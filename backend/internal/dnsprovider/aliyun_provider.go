package dnsprovider

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/alibabacloud-go/tea/dara"
	"github.com/go-acme/alidns-20150109/v5/client"
	"github.com/go-acme/lego/v5/challenge/dns01"
)

type aliyunRecordAPI interface {
	Add(context.Context, string, string, string) (string, error)
	Describe(context.Context, string, string, string) (aliyunRecordInfo, error)
	Delete(context.Context, string) error
}

type aliyunRecordInfo struct {
	RecordID   string
	DomainName string
	RR         string
	Type       string
	Value      string
}

type aliyunRecords struct{ client *client.Client }

func (a aliyunRecords) Add(ctx context.Context, zone, rr, value string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, aliyunAuthorizationTimeout)
	defer cancel()
	request := new(client.AddDomainRecordRequest).SetDomainName(zone).SetRR(rr).
		SetType("TXT").SetValue(value).SetTTL(aliyunAuthorizationTTL)
	response, err := client.AddDomainRecordWithContext(ctx, a.client, request, &dara.RuntimeOptions{})
	if err != nil {
		return "", err
	}
	if response == nil || response.Body == nil || response.Body.RecordId == nil || strings.TrimSpace(*response.Body.RecordId) == "" {
		return "", errors.New("阿里云未返回新增记录 ID，状态未确认，请人工核对本次记录")
	}
	return strings.TrimSpace(*response.Body.RecordId), nil
}

func (a aliyunRecords) Describe(ctx context.Context, zone, rr, recordID string) (aliyunRecordInfo, error) {
	zone = normalizeAuthorizationDomain(zone)
	rr = strings.TrimSpace(rr)
	recordID = strings.TrimSpace(recordID)
	if zone == "" || rr == "" || recordID == "" {
		return aliyunRecordInfo{}, errors.New("精确查询临时记录缺少托管区、主机记录或记录 ID")
	}
	// 按唯一托管区查询，兼容仅授予域名资源权限的 RAM 策略，不扩大账号可见范围。
	response, err := client.DescribeDomainRecordsWithContext(
		ctx,
		a.client,
		new(client.DescribeDomainRecordsRequest).SetDomainName(zone).SetRRKeyWord(rr).
			SetTypeKeyWord("TXT").SetSearchMode("EXACT").SetPageNumber(1).SetPageSize(100),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return aliyunRecordInfo{}, err
	}
	if response == nil || response.Body == nil || response.Body.DomainRecords == nil {
		return aliyunRecordInfo{}, errors.New("阿里云未返回临时 TXT 记录详情")
	}
	for _, record := range response.Body.DomainRecords.Record {
		if record == nil || strings.TrimSpace(dereferenceString(record.RecordId)) != recordID {
			continue
		}
		return aliyunRecordInfo{
			RecordID:   recordID,
			DomainName: normalizeAuthorizationDomain(dereferenceString(record.DomainName)),
			RR:         strings.TrimSpace(dereferenceString(record.RR)),
			Type:       strings.TrimSpace(dereferenceString(record.Type)),
			Value:      dereferenceString(record.Value),
		}, nil
	}
	return aliyunRecordInfo{}, errors.New("限定托管区查询未返回本次创建的临时 TXT 记录")
}

func (a aliyunRecords) Delete(ctx context.Context, id string) error {
	return deleteAuthorizationRecord(ctx, a.client, id)
}

func newAliyunRecordAPI(credentials map[string]string) (aliyunRecordAPI, error) {
	dnsClient, err := newAliyunClient(credentials)
	if err != nil {
		return nil, err
	}
	return aliyunRecords{client: dnsClient}, nil
}

// newAliyunAuthorizationRecordAPI 仅供短时权限探测使用，不改变 ACME DNS-01 的客户端策略。
func newAliyunAuthorizationRecordAPI(credentials map[string]string) (aliyunRecordAPI, error) {
	dnsClient, err := newAliyunAuthorizationClient(credentials)
	if err != nil {
		return nil, err
	}
	return aliyunRecords{client: dnsClient}, nil
}

// 不使用自动发现托管区的上游适配器，也不沿 CNAME 自动扩大写入范围。
type scopedAliyunProvider struct {
	credentials   map[string]string
	newAPI        func(map[string]string) (aliyunRecordAPI, error)
	api           aliyunRecordAPI
	mu            sync.Mutex
	records       map[[32]byte]aliyunPendingRecord
	challengeInfo func(context.Context, string, string) (string, string, string)
	zoneResolver  AliyunZoneResolver
}

type aliyunPendingRecord struct {
	ID   string
	Zone string
}

func newScopedAliyunProvider(credentials map[string]string) (*scopedAliyunProvider, error) {
	cleaned, err := ValidateCredentials(Aliyun, credentials)
	if err != nil {
		return nil, err
	}
	return &scopedAliyunProvider{
		credentials: cleaned, newAPI: newAliyunRecordAPI,
		records:       make(map[[32]byte]aliyunPendingRecord),
		challengeInfo: aliyunChallengeInfo,
		zoneResolver:  newDefaultAliyunZoneResolver(),
	}, nil
}

func aliyunChallengeInfo(ctx context.Context, domainName, keyAuth string) (string, string, string) {
	info := dns01.GetChallengeInfo(ctx, domainName, keyAuth)
	return info.FQDN, info.EffectiveFQDN, info.Value
}

func (p *scopedAliyunProvider) Present(ctx context.Context, name, token, keyAuth string) error {
	name = normalizeAuthorizationDomain(name)
	if p == nil {
		return errors.New("阿里云 DNS Provider 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	zone, err := resolveAliyunTargetDomain(ctx, name, p.zoneResolver)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	base := strings.TrimPrefix(name, "*.")
	fqdn, effective, value := p.challengeInfo(ctx, base, keyAuth)
	expected := "_acme-challenge." + base
	if normalizeAuthorizationDomain(fqdn) != expected || normalizeAuthorizationDomain(effective) != expected {
		return errors.New("DNS 挑战存在 CNAME 重定向，已拒绝自动跟随；请显式配置受限的 CNAME 委派服务商")
	}
	rr := strings.TrimSuffix(expected, "."+zone)
	if rr == expected {
		return errors.New("DNS 挑战记录不属于显式托管区")
	}
	key := sha256.Sum256([]byte(name + "\x00" + token + "\x00" + keyAuth))
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.records[key]; ok {
		return errors.New("同一 DNS 挑战已有待清理记录，拒绝重复新增")
	}
	if p.api == nil {
		if p.newAPI == nil {
			return errors.New("阿里云 DNS 客户端工厂未初始化")
		}
		api, err := p.newAPI(p.credentials)
		if err != nil {
			return err
		}
		p.api = api
	}
	id, err := p.api.Add(ctx, zone, rr, value)
	if err != nil {
		return err
	}
	p.records[key] = aliyunPendingRecord{ID: id, Zone: zone}
	return nil
}

func (p *scopedAliyunProvider) CleanUp(ctx context.Context, name, token, keyAuth string) error {
	if p == nil {
		return errors.New("阿里云 DNS Provider 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := sha256.Sum256([]byte(normalizeAuthorizationDomain(name) + "\x00" + token + "\x00" + keyAuth))
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.records[key]
	if !ok {
		return nil
	}
	if p.api == nil {
		return errors.New("阿里云 DNS 临时记录状态不完整，已拒绝清理请求")
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aliyunAuthorizationTimeout)
	defer cancel()
	if err := p.api.Delete(cleanupCtx, record.ID); err != nil {
		return err
	}
	delete(p.records, key)
	return nil
}

func (p *scopedAliyunProvider) Timeout() (time.Duration, time.Duration) {
	return 120 * time.Second, 2 * time.Second
}
