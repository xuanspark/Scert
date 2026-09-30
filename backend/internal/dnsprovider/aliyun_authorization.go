package dnsprovider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/go-acme/alidns-20150109/v5/client"
)

const (
	aliyunDefaultRegion        = "cn-hangzhou"
	aliyunAuthorizationTTL     = 600
	aliyunAuthorizationTimeout = 30 * time.Second
	aliyunAPICallTimeout       = 2 * time.Second
)

// AuthorizationDomain 描述一个阿里云 DNS 根域名的授权探测结果。
type AuthorizationDomain struct {
	Domain   string `json:"domain"`
	Listed   bool   `json:"listed"`
	Writable bool   `json:"writable"`
	Readable bool   `json:"readable"`
	Cleanup  bool   `json:"cleanup"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// AuthorizationReport 区分 API 可见范围和经过实际写入证明的范围。
type AuthorizationReport struct {
	Provider        string                `json:"provider"`
	RequestedDomain string                `json:"requested_domain,omitempty"`
	MatchedDomain   string                `json:"matched_domain,omitempty"`
	RecordName      string                `json:"record_name,omitempty"`
	Domains         []AuthorizationDomain `json:"domains"`
	Verified        bool                  `json:"verified"`
	Diagnosis       string                `json:"diagnosis,omitempty"`
	CheckedAt       time.Time             `json:"checked_at"`
}

// AliyunAuthorizationChecker 负责证明 RAM 账号对用户指定域名的实际写权限。
type AliyunAuthorizationChecker struct {
	now              func() time.Time
	verifyDomainFunc func(context.Context, string, map[string]string) error
	recordAPI        func(map[string]string) (aliyunRecordAPI, error)
	zoneResolver     AliyunZoneResolver
}

func NewAliyunAuthorizationChecker() *AliyunAuthorizationChecker {
	return &AliyunAuthorizationChecker{
		now:          time.Now,
		recordAPI:    newAliyunAuthorizationRecordAPI,
		zoneResolver: newDefaultAliyunZoneResolver(),
	}
}

func (c *AliyunAuthorizationChecker) Supports(providerID string) bool {
	return strings.EqualFold(strings.TrimSpace(providerID), Aliyun)
}

// Verify 仅针对本次 SOA 识别出的唯一权威托管区执行一次写入和精确清理。
func (c *AliyunAuthorizationChecker) Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (report AuthorizationReport, resultErr error) {
	requestedDomain = normalizeAuthorizationDomain(requestedDomain)
	report = AuthorizationReport{Provider: Aliyun, RequestedDomain: requestedDomain, CheckedAt: c.currentTime().UTC()}
	if c == nil {
		return report, errors.New("阿里云权限探测器未初始化")
	}
	verificationCtx, cancel := permissionVerificationContext(ctx)
	defer cancel()
	zone, err := c.resolveTargetDomain(verificationCtx, requestedDomain)
	if err != nil {
		return report, err
	}
	return c.verifyResolved(verificationCtx, requestedDomain, zone, credentials, report)
}

func (c *AliyunAuthorizationChecker) verifyResolved(ctx context.Context, requestedDomain, zone string, credentials map[string]string, report AuthorizationReport) (result AuthorizationReport, resultErr error) {
	result = report
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Domains = []AuthorizationDomain{{Domain: zone, Status: "待验证"}}
	if c == nil || c.recordAPI == nil {
		return result, errors.New("阿里云权限探测器未初始化")
	}
	cleaned, err := ValidateCredentials(Aliyun, credentials)
	if err != nil {
		return result, err
	}
	api, err := c.recordAPI(cleaned)
	if err != nil {
		return result, err
	}
	suffix, err := randomAuthorizationLabel()
	if err != nil {
		return result, err
	}
	value, err := randomAuthorizationValue(32)
	if err != nil {
		return result, err
	}
	rr := "_scert-authorization-" + suffix
	base := strings.TrimPrefix(requestedDomain, "*.")
	if base != zone {
		rr += "." + strings.TrimSuffix(base, "."+zone)
	}
	result.RecordName = rr + "." + zone + "."
	addCtx, addCancel := context.WithTimeout(ctx, aliyunAPICallTimeout)
	recordID, err := api.Add(addCtx, zone, rr, value)
	addCancel()
	if err != nil {
		markAuthorizationFailure(&result, zone, err)
		result.Diagnosis = DiagnoseAliyunError(err, zone)
		return result, err
	}
	result.Domains[0].Writable = true
	result.MatchedDomain = zone
	defer func() {
		cleanupBase := context.WithoutCancel(ctx)
		cleanupContext, cleanupCancel := context.WithTimeout(cleanupBase, aliyunAPICallTimeout)
		defer cleanupCancel()
		if cleanupErr := api.Delete(cleanupContext, recordID); cleanupErr != nil {
			cleanupErr = fmt.Errorf("清理授权探测 TXT 记录失败：%w", cleanupErr)
			markAuthorizationFailure(&result, zone, cleanupErr)
			result.Verified = false
			if resultErr == nil {
				resultErr = cleanupErr
			} else {
				resultErr = errors.Join(resultErr, cleanupErr)
			}
			return
		}
		result.Domains[0].Cleanup = true
	}()
	describeCtx, describeCancel := context.WithTimeout(ctx, aliyunAPICallTimeout)
	info, err := api.Describe(describeCtx, zone, rr, recordID)
	describeCancel()
	if err != nil {
		resultErr = fmt.Errorf("查询授权探测 TXT 记录失败：%w", err)
		markAuthorizationFailure(&result, zone, resultErr)
		result.Diagnosis = DiagnoseAliyunError(err, zone)
		return result, resultErr
	}
	if !matchesAliyunAuthorizationRecord(info, recordID, zone, rr, value) {
		resultErr = errors.New("阿里云返回的临时 TXT 记录与本次验证不一致")
		markAuthorizationFailure(&result, zone, resultErr)
		return result, resultErr
	}
	result.Domains[0].Readable = true
	result.Domains[0].Status = "可操作"
	result.Verified = true
	return result, nil
}

func matchesAliyunAuthorizationRecord(info aliyunRecordInfo, recordID, zone, rr, value string) bool {
	return strings.TrimSpace(info.RecordID) == strings.TrimSpace(recordID) &&
		normalizeAuthorizationDomain(info.DomainName) == normalizeAuthorizationDomain(zone) &&
		strings.EqualFold(strings.TrimSpace(info.RR), strings.TrimSpace(rr)) &&
		strings.EqualFold(strings.TrimSpace(info.Type), "TXT") &&
		info.Value == value
}

// VerifyDomain 每次重新证明写入和清理能力，不使用跨请求缓存掩盖撤权。
func (c *AliyunAuthorizationChecker) VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error {
	if !c.Supports(providerID) {
		return nil
	}
	if c == nil {
		return errors.New("阿里云权限探测器未初始化")
	}
	verificationCtx, cancel := permissionVerificationContext(ctx)
	defer cancel()
	if err := verificationCtx.Err(); err != nil {
		return err
	}
	requestedDomain = normalizeAuthorizationDomain(requestedDomain)
	zone, err := c.resolveTargetDomain(verificationCtx, requestedDomain)
	if err != nil {
		return err
	}
	if _, err := ValidateCredentials(Aliyun, credentials); err != nil {
		return err
	}
	if c.verifyDomainFunc != nil {
		return c.verifyDomainFunc(verificationCtx, requestedDomain, credentials)
	}
	report := AuthorizationReport{Provider: Aliyun, RequestedDomain: requestedDomain, CheckedAt: c.currentTime().UTC()}
	report, err = c.verifyResolved(verificationCtx, requestedDomain, zone, credentials, report)
	if err != nil {
		return err
	}
	if !report.Verified {
		return errors.New("阿里云 DNS 域名授权未通过")
	}
	return nil
}

func (c *AliyunAuthorizationChecker) resolveTargetDomain(ctx context.Context, requestedDomain string) (string, error) {
	if c == nil {
		return "", errors.New("阿里云权限探测器未初始化")
	}
	return resolveAliyunTargetDomain(ctx, requestedDomain, c.zoneResolver)
}

func permissionVerificationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, PermissionVerificationTimeoutFor(Aliyun))
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
	return newAliyunClientWithRetryOptions(credentials, nil)
}

// newAliyunAuthorizationClient 为权限探测建立独立的单次调用客户端。
// 探测流程有严格的阶段时限，不能因 SDK 重试或退避拉长用户等待；证书签发仍使用普通客户端策略。
func newAliyunAuthorizationClient(credentials map[string]string) (*client.Client, error) {
	return newAliyunClientWithRetryOptions(credentials, &dara.RetryOptions{
		Retryable:   false,
		MaxAttempts: 1,
	})
}

func newAliyunClientWithRetryOptions(credentials map[string]string, retryOptions *dara.RetryOptions) (*client.Client, error) {
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
	if retryOptions != nil {
		config.SetRetryOptions(retryOptions)
	}
	result, err := client.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("创建阿里云 DNS 客户端失败：%w", err)
	}
	return result, nil
}

func deleteAuthorizationRecord(ctx context.Context, dnsClient *client.Client, recordID string) error {
	if strings.TrimSpace(recordID) == "" {
		return errors.New("授权探测记录 ID 为空")
	}
	_, err := client.DeleteDomainRecordWithContext(ctx, dnsClient, new(client.DeleteDomainRecordRequest).SetRecordId(recordID), &dara.RuntimeOptions{})
	return err
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

func randomAuthorizationLabel() (string, error) {
	// 主机记录标签不能以连字符结尾；完整编码随机字节，避免替换字符造成分布偏差。
	data := make([]byte, 18)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func dereferenceString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// DiagnoseAliyunError 对阿里云 API 返回的错误进行分类并提供明确的修复指导。
func DiagnoseAliyunError(err error, domainName string) string {
	var reconciliation *aliyunReconciliationError
	if errors.As(err, &reconciliation) {
		return fmt.Sprintf("阿里云授权探测创建响应异常，临时 TXT 记录 %s 的清理尚未确认，请核对该记录；本次权限验证未通过", reconciliation.recordName)
	}
	if err == nil {
		return ""
	}
	msg := err.Error()
	lowered := strings.ToLower(msg)
	if strings.Contains(lowered, "invalidrr.format") {
		return "阿里云拒绝了授权探测记录名格式，本次验证未通过；这不代表 RAM 权限不足，请检查系统生成的记录名，不要扩大账号权限"
	}
	if strings.Contains(msg, "权威 DNS 托管区") || strings.Contains(lowered, "soa lookup") {
		return "未能从公开 DNS 的 SOA 记录识别该域名唯一的权威托管区，系统未构造阿里云客户端，也未创建临时 TXT 记录。请检查域名是否已正确委派并可被公网 DNS 查询后重试"
	}
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
