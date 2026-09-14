package dnsprovider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/go-acme/alidns-20150109/v5/client"
)

const (
	aliyunDefaultRegion        = "cn-hangzhou"
	aliyunAuthorizationTTL     = 60
	aliyunAuthorizationTimeout = 30 * time.Second
)

// AuthorizationDomain 描述一个阿里云 DNS 根域名的授权探测结果。
type AuthorizationDomain struct {
	Domain   string `json:"domain"`
	Listed   bool   `json:"listed"`
	Writable bool   `json:"writable"`
	Cleanup  bool   `json:"cleanup"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// AuthorizationReport 区分 API 可见范围和经过实际写入证明的范围。
type AuthorizationReport struct {
	Provider        string                `json:"provider"`
	RequestedDomain string                `json:"requested_domain,omitempty"`
	MatchedDomain   string                `json:"matched_domain,omitempty"`
	Domains         []AuthorizationDomain `json:"domains"`
	Verified        bool                  `json:"verified"`
	CheckedAt       time.Time             `json:"checked_at"`
}

// AliyunAuthorizationChecker 负责发现并证明 RAM 账号对阿里云 DNS 根域名的实际写权限。
type AliyunAuthorizationChecker struct {
	now func() time.Time
}

func NewAliyunAuthorizationChecker() *AliyunAuthorizationChecker {
	return &AliyunAuthorizationChecker{now: time.Now}
}

func (c *AliyunAuthorizationChecker) Supports(providerID string) bool {
	return strings.EqualFold(strings.TrimSpace(providerID), Aliyun)
}

// Discover 只读取阿里云账号可见的根域名，不把读取权限误判为写入权限。
func (c *AliyunAuthorizationChecker) Discover(ctx context.Context, credentials map[string]string) (AuthorizationReport, error) {
	report := AuthorizationReport{Provider: Aliyun, CheckedAt: c.currentTime().UTC()}
	client, err := newAliyunClient(credentials)
	if err != nil {
		return report, err
	}
	domains, err := listAliyunDomains(ctx, client)
	if err != nil {
		return report, fmt.Errorf("读取阿里云 DNS 授权域名失败：%w", err)
	}
	for _, name := range domains {
		report.Domains = append(report.Domains, AuthorizationDomain{
			Domain: name, Listed: true, Status: "可见但尚未证明可写",
		})
	}
	return report, nil
}

// Verify 对目标域名所属的最长托管根域名执行最小写入和精确清理。
func (c *AliyunAuthorizationChecker) Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (AuthorizationReport, error) {
	requestedDomain = normalizeAuthorizationDomain(requestedDomain)
	report, err := c.Discover(ctx, credentials)
	report.RequestedDomain = requestedDomain
	if err != nil {
		return report, err
	}
	if !domain.IsValidName(requestedDomain) {
		return report, fmt.Errorf("待验证域名格式无效：%s", requestedDomain)
	}
	matched := matchManagedDomain(requestedDomain, report.Domains)
	if matched == "" {
		return report, fmt.Errorf("阿里云 RAM 账号未发现域名 %s 所属的托管根域名", requestedDomain)
	}
	report.MatchedDomain = matched
	for index := range report.Domains {
		if report.Domains[index].Domain == matched {
			report.Domains[index].Status = "正在证明写入权限"
		}
	}

	client, err := newAliyunClient(credentials)
	if err != nil {
		return report, err
	}
	recordID, err := addAuthorizationRecord(ctx, client, matched)
	if err != nil {
		markAuthorizationFailure(&report, matched, fmt.Errorf("创建授权探测 TXT 记录失败：%w", err))
		return report, err
	}
	for index := range report.Domains {
		if report.Domains[index].Domain == matched {
			report.Domains[index].Writable = true
		}
	}

	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), aliyunAuthorizationTimeout)
	defer cancel()
	if err := deleteAuthorizationRecord(cleanupContext, client, recordID); err != nil {
		markAuthorizationFailure(&report, matched, fmt.Errorf("清理授权探测 TXT 记录失败：%w", err))
		return report, err
	}
	for index := range report.Domains {
		if report.Domains[index].Domain == matched {
			report.Domains[index].Cleanup = true
			report.Domains[index].Status = "可操作"
		}
	}
	report.Verified = true
	return report, nil
}

// VerifyDomain 实现证书服务使用的最小授权检查接口。
func (c *AliyunAuthorizationChecker) VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error {
	if !c.Supports(providerID) {
		return nil
	}
	report, err := c.Verify(ctx, requestedDomain, credentials)
	if err != nil {
		return err
	}
	if !report.Verified {
		return errors.New("阿里云 DNS 域名授权未通过")
	}
	return nil
}

func (c *AliyunAuthorizationChecker) currentTime() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

func newAliyunClient(credentials map[string]string) (*client.Client, error) {
	cleaned, err := ValidateCredentials(Aliyun, credentials)
	if err != nil {
		return nil, err
	}
	config := new(openapi.Config).SetRegionId(aliyunDefaultRegion).
		SetAccessKeyId(cleaned["access_key_id"]).
		SetAccessKeySecret(cleaned["access_key_secret"])
	if token := cleaned["security_token"]; token != "" {
		config.SetSecurityToken(token)
	}
	result, err := client.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("创建阿里云 DNS 客户端失败：%w", err)
	}
	return result, nil
}

func listAliyunDomains(ctx context.Context, dnsClient *client.Client) ([]string, error) {
	if dnsClient == nil {
		return nil, errors.New("阿里云 DNS 客户端未初始化")
	}
	var result []string
	for page := int64(1); ; page++ {
		request := new(client.DescribeDomainsRequest).SetPageNumber(page).SetPageSize(100)
		response, err := client.DescribeDomainsWithContext(ctx, dnsClient, request, &dara.RuntimeOptions{})
		if err != nil {
			return nil, err
		}
		if response == nil || response.Body == nil {
			return nil, errors.New("阿里云 DNS 返回空的域名列表")
		}
		if response.Body.Domains != nil {
			for _, item := range response.Body.Domains.Domain {
				if item == nil || item.DomainName == nil {
					continue
				}
				name := normalizeAuthorizationDomain(*item.DomainName)
				if name != "" {
					result = append(result, name)
				}
			}
		}
		pageSize := dereferenceInt64(response.Body.PageSize)
		total := dereferenceInt64(response.Body.TotalCount)
		if pageSize <= 0 || page*pageSize >= total {
			break
		}
	}
	sort.Strings(result)
	return uniqueStrings(result), nil
}

func addAuthorizationRecord(ctx context.Context, dnsClient *client.Client, zone string) (string, error) {
	suffix, err := randomAuthorizationValue(18)
	if err != nil {
		return "", fmt.Errorf("生成授权探测标识失败：%w", err)
	}
	value, err := randomAuthorizationValue(32)
	if err != nil {
		return "", fmt.Errorf("生成授权探测值失败：%w", err)
	}
	request := new(client.AddDomainRecordRequest).
		SetDomainName(zone).
		SetRR("_scert-authorization-" + suffix).
		SetType("TXT").
		SetValue(value).
		SetTTL(aliyunAuthorizationTTL)
	response, err := client.AddDomainRecordWithContext(ctx, dnsClient, request, &dara.RuntimeOptions{})
	if err != nil {
		return "", err
	}
	if response == nil || response.Body == nil || response.Body.RecordId == nil || strings.TrimSpace(*response.Body.RecordId) == "" {
		return "", errors.New("阿里云未返回授权探测记录 ID")
	}
	return strings.TrimSpace(*response.Body.RecordId), nil
}

func deleteAuthorizationRecord(ctx context.Context, dnsClient *client.Client, recordID string) error {
	if strings.TrimSpace(recordID) == "" {
		return errors.New("授权探测记录 ID 为空")
	}
	_, err := client.DeleteDomainRecordWithContext(ctx, dnsClient, new(client.DeleteDomainRecordRequest).SetRecordId(recordID), &dara.RuntimeOptions{})
	return err
}

func matchManagedDomain(requested string, domains []AuthorizationDomain) string {
	requested = normalizeAuthorizationDomain(requested)
	best := ""
	for _, item := range domains {
		candidate := normalizeAuthorizationDomain(item.Domain)
		if candidate == "" || (requested != candidate && !strings.HasSuffix(requested, "."+candidate)) {
			continue
		}
		if len(candidate) > len(best) {
			best = candidate
		}
	}
	return best
}

func markAuthorizationFailure(report *AuthorizationReport, domainName string, err error) {
	for index := range report.Domains {
		if report.Domains[index].Domain == domainName {
			report.Domains[index].Status = "不可操作"
			report.Domains[index].Error = err.Error()
		}
	}
}

func normalizeAuthorizationDomain(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func randomAuthorizationValue(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func dereferenceInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
