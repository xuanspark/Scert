package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) domainDNSRepository(c *gin.Context) (repository.DomainDNSAccountRepository, bool) {
	repo, ok := s.projectMembers.(repository.DomainDNSAccountRepository)
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "域名 DNS 账户绑定服务尚未就绪", nil)
		return nil, false
	}
	if !domain.ValidProjectID(c.Param("id")) || c.Request.URL.RawQuery != "" {
		s.writeError(c, http.StatusBadRequest, "域名 DNS 账户绑定参数无效", nil)
		return nil, false
	}
	return repo, true
}

func (s *Server) writeDomainDNSAccountError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrProjectAccessDenied):
		s.writeError(c, http.StatusForbidden, "无权操作此域名的 DNS 账户绑定", nil)
	case errors.Is(err, repository.ErrDNSBindingConflict), errors.Is(err, repository.ErrDNSAccountConflict):
		s.writeError(c, http.StatusConflict, "域名绑定或 DNS 账户已变更，请刷新后重试", nil)
	case errors.Is(err, repository.ErrDNSBindingBusy):
		s.writeError(c, http.StatusConflict, "域名存在未结束的任务，暂不能变更 DNS 账户", nil)
	case errors.Is(err, repository.ErrDNSBindingInvalid):
		s.writeError(c, http.StatusUnprocessableEntity, "域名验证方式或 DNS 账户状态不允许绑定", nil)
	default:
		// 驱动错误可能含内部标识；只返回固定提示，不记录原始错误。
		s.writeError(c, http.StatusServiceUnavailable, "保存或读取 DNS 账户绑定失败，请刷新后核对", nil)
	}
}

func (s *Server) getDomainDNSAccount(c *gin.Context) {
	repo, ok := s.domainDNSRepository(c)
	if !ok {
		return
	}
	item, err := repo.GetDomainDNSAccountBinding(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeDomainDNSAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) setDomainDNSAccount(c *gin.Context) {
	repo, ok := s.domainDNSRepository(c)
	if !ok {
		return
	}
	var input struct {
		AccountID         *string `json:"dns_account_id"`
		ExpectedBindingID *string `json:"expected_binding_id"`
		AccountRevision   *int64  `json:"account_revision"`
	}
	// 三个字段必须显式提供，禁止遗漏字段或 null 被解释成解除已有绑定。
	if bindJSON(c, &input, jsonRequestBodyLimit) != nil || input.AccountID == nil || input.ExpectedBindingID == nil || input.AccountRevision == nil {
		s.writeError(c, http.StatusBadRequest, "请明确提供 DNS 账户、当前绑定标识和账户版本", nil)
		return
	}
	accountID, expectedID, revision := *input.AccountID, *input.ExpectedBindingID, *input.AccountRevision
	if (expectedID != "" && !domain.ValidProjectID(expectedID)) ||
		(accountID == "" && revision != 0) ||
		(accountID != "" && (!domain.ValidProjectID(accountID) || revision < 1 || revision > 2147483647)) {
		s.writeError(c, http.StatusBadRequest, "DNS 账户绑定标识或版本无效", nil)
		return
	}
	item, err := repo.SetDomainDNSAccountBinding(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"), accountID, expectedID, revision)
	if err != nil {
		s.writeDomainDNSAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
