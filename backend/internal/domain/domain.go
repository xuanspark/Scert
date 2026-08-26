package domain

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var domainPattern = regexp.MustCompile(`^(?i)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

type CertificateType string

const (
	CertificateSingle   CertificateType = "single"
	CertificateWildcard CertificateType = "wildcard"
	CertificateSAN      CertificateType = "san"
)

type Domain struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	CertificateType CertificateType `json:"certificate_type"`
	SANs            []string        `json:"sans"`
	Status          string          `json:"status"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

func NewDomain(name string, certificateType CertificateType, sans []string) (Domain, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !IsValidName(name) {
		return Domain{}, fmt.Errorf("域名格式无效：%s", name)
	}
	if certificateType != CertificateSingle && certificateType != CertificateWildcard && certificateType != CertificateSAN {
		return Domain{}, fmt.Errorf("证书类型无效：%s", certificateType)
	}
	cleanSANs := normalizeSANs(sans)
	if certificateType == CertificateSAN {
		cleanSANs = normalizeSANs(append([]string{name}, cleanSANs...))
	}
	if certificateType == CertificateSAN && len(cleanSANs) < 2 {
		return Domain{}, fmt.Errorf("SAN 证书至少需要两个域名")
	}
	for _, san := range cleanSANs {
		if !IsValidName(san) {
			_, err := NewDomain(san, CertificateSingle, nil)
			return Domain{}, err
		}
	}
	if certificateType != CertificateSAN {
		cleanSANs = []string{name}
	}
	if certificateType == CertificateWildcard && !strings.HasPrefix(name, "*.") {
		return Domain{}, fmt.Errorf("泛域名必须以 *. 开头")
	}
	if certificateType != CertificateWildcard && strings.HasPrefix(name, "*.") {
		return Domain{}, fmt.Errorf("只有泛域名证书类型允许使用 *. 域名")
	}
	now := time.Now().UTC()
	return Domain{ID: NewID(), Name: name, CertificateType: certificateType, SANs: cleanSANs, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
}

func IsValidName(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if strings.HasPrefix(name, "*.") {
		name = strings.TrimPrefix(name, "*.")
	}
	return domainPattern.MatchString(name)
}

func normalizeSANs(sans []string) []string {
	seen := make(map[string]struct{}, len(sans))
	result := make([]string, 0, len(sans))
	for _, item := range sans {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
