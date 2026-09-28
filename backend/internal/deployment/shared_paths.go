package deployment

import (
	"errors"
	"fmt"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

var ErrSharedPathConflict = errors.New("多个域名的远程证书路径冲突，请使用按域名区分的证书、证书链和私钥路径")

// 比较实际展开并规范化后的全部文件路径，不仅检查模板是否含有域名占位符。
// 错误不携带其他域名名称、资源标识或远程私钥路径。
func ValidateServerDomainPaths(server Server, domains []domain.Domain) error {
	paths := make([]string, 0, len(domains)*3)
	for _, item := range domains {
		resolved, err := ResolveServerPaths(server, item.Name, string(item.CertificateType))
		if err != nil {
			return fmt.Errorf("关联域名的远程证书路径无效：%w", err)
		}
		current := []string{resolved.CertificatePath, resolved.CertificateChainPath, resolved.PrivateKeyPath}
		for _, candidate := range current {
			for _, existing := range paths {
				if candidate == existing || strings.HasPrefix(candidate, existing+"/") || strings.HasPrefix(existing, candidate+"/") {
					return ErrSharedPathConflict
				}
			}
		}
		paths = append(paths, current...)
	}
	return nil
}
