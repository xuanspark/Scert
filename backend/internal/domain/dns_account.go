package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DNSAccountActive             = "active"
	DNSAccountDisabled           = "disabled"
	DNSAccountUnverified         = "unverified"
	DNSAccountVerified           = "verified"
	DNSAccountVerificationFailed = "failed"
)

// DNSAccount 只保存账户元数据和秘密引用；序列化时不暴露秘密仓库位置。
// 归属和服务商在创建后固定，轮换必须产生新引用，版本号用于拒绝旧验证结果。
type DNSAccount struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	ProjectID         string    `json:"project_id"`
	OwnerUserID       string    `json:"owner_user_id"`
	Provider          string    `json:"provider"`
	CredentialRef     string    `json:"-"`
	Status            string    `json:"status"`
	VerificationState string    `json:"verification_state"`
	Revision          int64     `json:"revision"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (a DNSAccount) Validate() error {
	if !ValidProjectID(a.ID) || !ValidProjectID(a.ProjectID) || !ValidProjectID(a.OwnerUserID) {
		return errors.New("DNS 账户标识、项目或归属用户无效")
	}
	if !utf8.ValidString(a.Name) || strings.TrimSpace(a.Name) == "" || utf8.RuneCountInString(a.Name) > 128 || strings.ContainsFunc(a.Name, unicode.IsControl) {
		return errors.New("DNS 账户名称必须为不含控制字符的 1～128 个字符")
	}
	if !ValidProjectID(a.Provider) {
		return errors.New("DNS 服务商标识无效")
	}
	if !strings.HasPrefix(a.CredentialRef, "secret://dns/") || len(a.CredentialRef) <= len("secret://dns/") || len(a.CredentialRef) > 255 || strings.ContainsFunc(a.CredentialRef, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return errors.New("DNS 凭据引用无效")
	}
	if a.Status != DNSAccountActive && a.Status != DNSAccountDisabled {
		return errors.New("DNS 账户状态无效")
	}
	if a.VerificationState != DNSAccountUnverified && a.VerificationState != DNSAccountVerified && a.VerificationState != DNSAccountVerificationFailed {
		return errors.New("DNS 账户验证状态无效")
	}
	if a.Revision < 1 || a.Revision > 2147483647 {
		return errors.New("DNS 账户版本无效")
	}
	return nil
}
