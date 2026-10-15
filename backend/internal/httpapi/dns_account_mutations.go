package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsverification"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type dnsAccountVerifier interface {
	Verify(context.Context, string, string, map[string]string) (dnsverification.Report, error)
}

func (s *Server) managedDNSRepository(c *gin.Context) (repository.DNSAccountRepository, bool) {
	repo, ok := s.projectMembers.(repository.DNSAccountRepository)
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "DNS 账户服务尚未就绪", nil)
	}
	return repo, ok
}

func (s *Server) writeDNSAccountError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrProjectAccessDenied):
		s.writeError(c, http.StatusForbidden, "无权操作此 DNS 账户", nil)
	case errors.Is(err, repository.ErrDNSAccountConflict):
		s.writeError(c, http.StatusConflict, "DNS 账户已变更，请刷新后重试", nil)
	case errors.Is(err, repository.ErrDNSAccountBusy):
		s.writeError(c, http.StatusConflict, "DNS 账户仍有执行租约，请等待任务及挑战清理完成", nil)
	default:
		// 驱动、秘密仓库和服务商的错误均可能携带凭据，不能回显或写入日志。
		s.writeError(c, http.StatusServiceUnavailable, "DNS 账户操作失败，请稍后重试", nil)
	}
}

func (s *Server) createManagedDNSAccount(c *gin.Context) {
	repo, ok := s.managedDNSRepository(c)
	if !ok {
		return
	}
	var request struct {
		Name        string            `json:"name"`
		ProjectID   string            `json:"project_id"`
		Provider    string            `json:"provider"`
		Credentials map[string]string `json:"credentials"`
	}
	if err := bindJSON(c, &request, setupRequestLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "DNS 账户参数无效", nil)
		return
	}
	if !s.authorizeProjectPermissions(c, request.ProjectID, domain.PermissionDNSManage) {
		return
	}
	item := domain.DNSAccount{
		ID: domain.NewID(), Name: strings.TrimSpace(request.Name), ProjectID: request.ProjectID,
		OwnerUserID: c.GetString(authenticatedActorKey), Provider: request.Provider,
		CredentialRef: "secret://dns/" + domain.NewID(), Status: domain.DNSAccountActive,
		VerificationState: domain.DNSAccountUnverified, Revision: 1,
	}
	if err := item.Validate(); err != nil {
		s.writeError(c, http.StatusBadRequest, "DNS 账户名称、项目或服务商无效", nil)
		return
	}
	if !s.storeManagedDNSCredential(c, item.Provider, item.CredentialRef, request.Credentials) {
		return
	}
	created, err := repo.CreateDNSAccount(c.Request.Context(), item.OwnerUserID, item)
	if err != nil {
		// 提交错误可能有歧义，不能猜测未提交并删除新秘密；后续引用回收统一处理。
		s.writeDNSAccountError(c, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (s *Server) storeManagedDNSCredential(c *gin.Context, provider, reference string, credentials map[string]string) bool {
	descriptor, err := dnsprovider.GetDescriptor(provider)
	if err != nil || !descriptor.Available {
		s.writeError(c, http.StatusBadRequest, "DNS 服务商不可用", nil)
		return false
	}
	cleaned, err := dnsprovider.ValidateCredentials(provider, credentials)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "DNS 凭据字段无效或不完整", nil)
		return false
	}
	if s.secrets == nil {
		s.writeError(c, http.StatusServiceUnavailable, "凭据存储尚未就绪", nil)
		return false
	}
	if err := s.secrets.Put(c.Request.Context(), reference, cleaned); err != nil {
		s.writeError(c, http.StatusServiceUnavailable, "保存 DNS 凭据失败", nil)
		return false
	}
	return true
}

func (s *Server) getManagedDNSAccount(c *gin.Context) {
	repo, ok := s.managedDNSRepository(c)
	if !ok {
		return
	}
	item, err := repo.GetVisibleDNSAccount(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) renameManagedDNSAccount(c *gin.Context) {
	repo, ok := s.managedDNSRepository(c)
	if !ok {
		return
	}
	var request struct {
		Name        string            `json:"name"`
		Revision    int64             `json:"revision"`
		Credentials map[string]string `json:"credentials"`
	}
	if err := bindJSON(c, &request, setupRequestLimit); err != nil || request.Revision < 1 {
		s.writeError(c, http.StatusBadRequest, "DNS 账户名称或版本无效", nil)
		return
	}
	// 先核对可见范围，避免借参数错误探测其他项目账户。
	item, err := repo.GetVisibleDNSAccount(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	if !s.authorizeProjectPermissions(c, item.ProjectID, domain.PermissionDNSManage) {
		return
	}
	if item.Revision != request.Revision {
		s.writeDNSAccountError(c, repository.ErrDNSAccountConflict)
		return
	}
	item.Name = strings.TrimSpace(request.Name)
	item.CredentialRef = "secret://dns/validation"
	if err := item.Validate(); err != nil {
		s.writeError(c, http.StatusBadRequest, "DNS 账户名称无效", nil)
		return
	}
	reference := ""
	if len(request.Credentials) > 0 {
		reference = "secret://dns/" + domain.NewID()
		if !s.storeManagedDNSCredential(c, item.Provider, reference, request.Credentials) {
			return
		}
	}
	updated, err := repo.UpdateDNSAccount(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"), request.Revision, item.Name, reference)
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) setManagedDNSAccountStatus(c *gin.Context) {
	repo, ok := s.managedDNSRepository(c)
	if !ok {
		return
	}
	var request struct {
		Enabled  *bool `json:"enabled"`
		Revision int64 `json:"revision"`
	}
	if err := bindJSON(c, &request, setupRequestLimit); err != nil || request.Enabled == nil || request.Revision < 1 {
		s.writeError(c, http.StatusBadRequest, "DNS 账户状态或版本无效", nil)
		return
	}
	item, err := repo.SetDNSAccountEnabled(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"), request.Revision, *request.Enabled)
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) rotateManagedDNSAccountCredential(c *gin.Context) {
	repo, ok := s.managedDNSRepository(c)
	if !ok {
		return
	}
	var request struct {
		Revision    int64             `json:"revision"`
		Credentials map[string]string `json:"credentials"`
	}
	if err := bindJSON(c, &request, setupRequestLimit); err != nil || request.Revision < 1 {
		s.writeError(c, http.StatusBadRequest, "DNS 凭据或版本无效", nil)
		return
	}
	item, err := repo.GetVisibleDNSAccount(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	if !s.authorizeProjectPermissions(c, item.ProjectID, domain.PermissionDNSManage) {
		return
	}
	if item.Revision != request.Revision {
		s.writeDNSAccountError(c, repository.ErrDNSAccountConflict)
		return
	}
	reference := "secret://dns/" + domain.NewID()
	if !s.storeManagedDNSCredential(c, item.Provider, reference, request.Credentials) {
		return
	}
	item, err = repo.RotateDNSAccountCredential(c.Request.Context(), c.GetString(authenticatedActorKey), item.ID, request.Revision, reference)
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	// 不删除旧凭据，仍有运行任务或旧配置引用时须由后续租约回收流程处理。
	c.JSON(http.StatusOK, item)
}

func (s *Server) verifyManagedDNSAccount(c *gin.Context) {
	repo, ok := s.managedDNSRepository(c)
	if !ok {
		return
	}
	var request struct {
		Revision   int64  `json:"revision"`
		TestDomain string `json:"test_domain"`
	}
	if err := bindJSON(c, &request, setupRequestLimit); err != nil || request.Revision < 1 || !domain.IsValidName(request.TestDomain) {
		s.writeError(c, http.StatusBadRequest, "DNS 验证域名或版本无效", nil)
		return
	}
	actor := c.GetString(authenticatedActorKey)
	item, err := repo.GetDNSAccountForVerification(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	if item.Revision != request.Revision {
		s.writeDNSAccountError(c, repository.ErrDNSAccountConflict)
		return
	}
	credentials, err := s.readDNSCredentials(c.Request.Context(), item.CredentialRef)
	if err != nil {
		s.writeError(c, http.StatusServiceUnavailable, "读取 DNS 凭据失败", nil)
		return
	}
	verifier := s.dnsAccountVerifier
	if verifier == nil {
		verifier = dnsprovider.NewTester(dnsprovider.NewLegoFactory())
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), dnsprovider.PermissionVerificationTimeoutFor(item.Provider))
	defer cancel()
	_, verificationErr := verifier.Verify(ctx, item.Provider, request.TestDomain, credentials)
	if verificationErr == nil {
		verificationErr = ctx.Err()
	}
	// 在线调用可能超时或断连，短独立上下文只用于记录实际结果，仍重新核对权限与版本。
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer recordCancel()
	updated, err := repo.RecordDNSAccountVerification(recordCtx, actor, item.ID, item.Revision, verificationErr == nil)
	if err != nil {
		s.writeDNSAccountError(c, err)
		return
	}
	if verificationErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "DNS 在线验证失败，请检查凭据、测试域名和权限", "account": updated})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verified": true, "account": updated})
}
