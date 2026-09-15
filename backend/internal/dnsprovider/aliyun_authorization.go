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
	aliyunAuthorizationTTL     = 600
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
	Diagnosis       string                `json:"diagnosis,omitempty"`
	CheckedAt       time.Time             `json:"checked_at"`
}

// AliyunAuthorizationChecker 负责发现并证明 RAM 账号对阿里云 DNS 根域名的实际写权限。
type AliyunAuthorizationChecker struct {
	now              func() time.Time
	verifyDomainFunc func(context.Context, string, map[string]string) error
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

// Inspect 只查询显式指定托管区的读取权限，不逐级探测父域、不写入 DNS，也不证明可写。
func (c *AliyunAuthorizationChecker) Inspect(ctx context.Context, requestedDomain string, credentials map[string]string) (AuthorizationReport, error) {
	requestedDomain = strings.TrimPrefix(normalizeAuthorizationDomain(requestedDomain), "*.")
	report := AuthorizationReport{Provider: Aliyun, RequestedDomain: requestedDomain, CheckedAt: c.currentTime().UTC()}
	if !domain.IsValidName(requestedDomain) {
		return report, fmt.Errorf("待检查托管区格式无效：%s", requestedDomain)
	}
	report.Domains = []AuthorizationDomain{{Domain: requestedDomain, Status: "等待读取权限检查"}}
	dnsClient, err := newAliyunClient(credentials)
	if err != nil {
		return report, err
	}
	request := new(client.DescribeDomainRecordsRequest).SetDomainName(requestedDomain).SetPageNumber(1).SetPageSize(1)
	response, err := client.DescribeDomainRecordsWithContext(ctx, dnsClient, request, &dara.RuntimeOptions{})
	if err != nil {
		report.Domains[0].Status = "读取未通过，可写性未验证"
		report.Domains[0].Error = err.Error()
		report.Diagnosis = DiagnoseAliyunError(err, requestedDomain)
		return report, fmt.Errorf("阿里云域名读取检查失败：%w", err)
	}
	if response == nil || response.Body == nil {
		return report, errors.New("阿里云未返回有效的域名读取结果")
	}
	report.Domains[0].Listed = true
	report.Domains[0].Status = "可读取，未验证写入和清理权限"
	// 读取成功必须保持 Verified=false，也不能写入授权缓存。
	return report, nil
}

// Verify 从目标域名到父级托管区逐级执行最小写入和精确清理。
func (c *AliyunAuthorizationChecker) Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (AuthorizationReport, error) {
	requestedDomain = normalizeAuthorizationDomain(requestedDomain)
	report := AuthorizationReport{Provider: Aliyun, RequestedDomain: requestedDomain, CheckedAt: c.currentTime().UTC()}
	if !domain.IsValidName(requestedDomain) {
		return report, fmt.Errorf("待验证域名格式无效：%s", requestedDomain)
	}
	for _, candidate := range authorizationZoneCandidates(requestedDomain) {
		report.Domains = append(report.Domains, AuthorizationDomain{
			Domain: candidate, Status: "待验证",
		})
	}
	client, err := newAliyunClient(credentials)
	if err != nil {
		return report, err
	}

	var lastErr error
	for index, candidate := range authorizationZoneCandidates(requestedDomain) {
		report.Domains[index].Status = "正在证明写入权限"
		recordID, probeErr := addAuthorizationRecord(ctx, client, candidate)
		if probeErr != nil {
			lastErr = probeErr
			report.Domains[index].Status = "不可操作"
			report.Domains[index].Error = probeErr.Error()
			continue
		}
		report.Domains[index].Writable = true
		report.MatchedDomain = candidate
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), aliyunAuthorizationTimeout)
		cleanupErr := deleteAuthorizationRecord(cleanupContext, client, recordID)
		cancel()
		if cleanupErr != nil {
			markAuthorizationFailure(&report, candidate, fmt.Errorf("清理授权探测 TXT 记录失败：%w", cleanupErr))
			return report, cleanupErr
		}
		report.Domains[index].Cleanup = true
		report.Domains[index].Status = "可操作"
		report.Verified = true
		return report, nil
	}
	if lastErr == nil {
		lastErr = errors.New("未找到可验证的阿里云 DNS 托管根域名")
	}
	report.Diagnosis = DiagnoseAliyunError(lastErr, requestedDomain)
	return report, fmt.Errorf("阿里云 RAM 账号无法写入域名 %s：%w（建议：%s）", requestedDomain, lastErr, report.Diagnosis)
}

// VerifyDomain 每次重新证明写入和清理能力，不使用跨请求缓存掩盖撤权。
func (c *AliyunAuthorizationChecker) VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error {
	if !c.Supports(providerID) {
		return nil
	}
	if c == nil {
		return errors.New("阿里云权限探测器未初始化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	requestedDomain = normalizeAuthorizationDomain(requestedDomain)
	if !domain.IsValidName(requestedDomain) {
		return fmt.Errorf("待验证域名格式无效：%s", requestedDomain)
	}
	if _, err := ValidateCredentials(Aliyun, credentials); err != nil {
		return err
	}
	if c.verifyDomainFunc != nil {
		return c.verifyDomainFunc(ctx, requestedDomain, credentials)
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

// InvalidateDomain 保留旧调用方接口；授权检查已不再缓存，无需额外失效操作。
func (c *AliyunAuthorizationChecker) InvalidateDomain(providerID, requestedDomain string, credentials map[string]string) {
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

func authorizationZoneCandidates(requested string) []string {
	requested = strings.TrimPrefix(normalizeAuthorizationDomain(requested), "*.")
	labels := strings.Split(requested, ".")
	result := make([]string, 0, len(labels)-1)
	for index := 0; index < len(labels)-1; index++ {
		candidate := strings.Join(labels[index:], ".")
		if domain.IsValidName(candidate) {
			result = append(result, candidate)
		}
	}
	return result
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

// DiagnoseAliyunError 对阿里云 API 返回的错误进行分类并提供明确的修复指导。
func DiagnoseAliyunError(err error, domainName string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lowered := strings.ToLower(msg)
	if strings.Contains(lowered, "forbidden.nopermission") || strings.Contains(lowered, "forbidden.ram") || strings.Contains(lowered, "accessdenied") || strings.Contains(lowered, "not authorized") {
		return fmt.Sprintf("当前阿里云 RAM 账号未获得域名 %s 所需的 DNS 操作权限。请管理员核对实际托管区资源 acs:alidns:*:*:domain/%s，按失败操作补充必要的读取、新增或删除权限；不要直接扩大为全部操作或全部域名权限", domainName, domainName)
	}
	if strings.Contains(lowered, "nosuchdomain") || strings.Contains(lowered, "invaliddomainname.noexist") {
		return fmt.Sprintf("域名 %s 未在当前阿里云账号下添加 DNS 托管，请先在阿里云云解析 DNS 控制台添加此域名", domainName)
	}
	if strings.Contains(lowered, "invalidaccesskeyid.notfound") || strings.Contains(lowered, "signaturedoesnotmatch") {
		return "阿里云 AccessKey ID 或 AccessKey Secret 无效，请检查系统 DNS 凭据配置"
	}
	if errors.Is(err, context.Canceled) {
		return "阿里云 RAM 权限验证已取消，当前权限尚未确认"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "阿里云 RAM 权限验证超时，请检查网络后重新验证"
	}
	return "阿里云 RAM 权限验证未通过，请检查网络、凭据和目标域名权限；不要将未确认状态视为已授权"
}
