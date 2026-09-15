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
	Describe(context.Context, string) (aliyunRecordInfo, error)
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

func (a aliyunRecords) Describe(ctx context.Context, recordID string) (aliyunRecordInfo, error) {
	response, err := client.DescribeDomainRecordInfoWithContext(
		ctx,
		a.client,
		new(client.DescribeDomainRecordInfoRequest).SetRecordId(recordID),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return aliyunRecordInfo{}, err
	}
	if response == nil || response.Body == nil {
		return aliyunRecordInfo{}, errors.New("阿里云未返回临时 TXT 记录详情")
	}
	return aliyunRecordInfo{
		RecordID:   strings.TrimSpace(dereferenceString(response.Body.RecordId)),
		DomainName: normalizeAuthorizationDomain(dereferenceString(response.Body.DomainName)),
		RR:         strings.TrimSpace(dereferenceString(response.Body.RR)),
		Type:       strings.TrimSpace(dereferenceString(response.Body.Type)),
		Value:      dereferenceString(response.Body.Value),
	}, nil
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

// 不使用自动发现托管区的上游适配器，也不沿 CNAME 自动扩大写入范围。
type scopedAliyunProvider struct {
	scope         AliyunScope
	api           aliyunRecordAPI
	mu            sync.Mutex
	records       map[[32]byte]string
	challengeInfo func(context.Context, string, string) (string, string, string)
}

func newScopedAliyunProvider(credentials map[string]string) (*scopedAliyunProvider, error) {
	scope, err := ParseAliyunScope(credentials)
	if err != nil {
		return nil, err
	}
	api, err := newAliyunRecordAPI(credentials)
	if err != nil {
		return nil, err
	}
	return &scopedAliyunProvider{scope: scope, api: api, records: make(map[[32]byte]string), challengeInfo: aliyunChallengeInfo}, nil
}

func aliyunChallengeInfo(ctx context.Context, domainName, keyAuth string) (string, string, string) {
	info := dns01.GetChallengeInfo(ctx, domainName, keyAuth)
	return info.FQDN, info.EffectiveFQDN, info.Value
}

func (p *scopedAliyunProvider) Present(ctx context.Context, name, token, keyAuth string) error {
	name = normalizeAuthorizationDomain(name)
	zone, err := p.scope.Resolve(name)
	// ACME 的通配符授权以去掉通配符的标识符进入 Provider；业务层已逐项验证 SAN。
	if err != nil && !strings.HasPrefix(name, "*.") {
		zone, err = p.scope.Resolve("*." + name)
	}
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
	id, err := p.api.Add(ctx, zone, rr, value)
	if err != nil {
		return err
	}
	p.records[key] = id
	return nil
}

func (p *scopedAliyunProvider) CleanUp(ctx context.Context, name, token, keyAuth string) error {
	key := sha256.Sum256([]byte(normalizeAuthorizationDomain(name) + "\x00" + token + "\x00" + keyAuth))
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.records[key]
	if !ok {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aliyunAuthorizationTimeout)
	defer cancel()
	if err := p.api.Delete(cleanupCtx, id); err != nil {
		return err
	}
	delete(p.records, key)
	return nil
}

func (p *scopedAliyunProvider) Timeout() (time.Duration, time.Duration) {
	return 120 * time.Second, 2 * time.Second
}
