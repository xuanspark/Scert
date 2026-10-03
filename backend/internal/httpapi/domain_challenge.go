package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func (s *Server) authorizeDomainChallenge(c *gin.Context, item domain.Domain) bool {
	if item.ChallengeType == domain.ChallengeHTTP01 {
		return true
	}
	if s.currentDNSProvider() == dnsprovider.Aliyun && !s.authorizeDNSCredentialUse(c, item.ProjectID) {
		return false
	}
	if err := s.validateDomainAliyunAuthorization(c.Request.Context(), append([]string{item.Name}, item.SANs...)...); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return false
	}
	return true
}

func (s *Server) authorizeIssueChallenge(c *gin.Context, item domain.Domain) bool {
	kind, err := domain.NormalizeChallengeType(item.ChallengeType, append([]string{item.Name}, item.SANs...))
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "证书验证方式无效", err)
		return false
	}
	return kind == domain.ChallengeHTTP01 || s.authorizeDNSCredentialUse(c, item.ResourceScope.Normalize().ProjectID)
}
