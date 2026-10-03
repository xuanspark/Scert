package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestDomainDNSExecutionLeaseMutationConflict(t *testing.T) {
	f, account := newDomainDNSAPIFixture(t)
	f.server.projectDomains = f.members
	if _, err := f.members.SetDomainDNSAccountBinding(t.Context(), "manager", "domain-a", account.ID, "", account.Revision); err != nil {
		t.Fatal(err)
	}
	item, err := repository.NewSQLDomainRepository(f.db).GetByID(t.Context(), "domain-a")
	if err != nil {
		t.Fatal(err)
	}
	tasks := repository.NewSQLTaskRepository(f.db)
	identity := domain.DNSExecutionIdentity{TaskID: domain.NewID(), AttemptID: domain.NewID(), AttemptNumber: 1}
	if err := tasks.CreateTask(t.Context(), repository.Task{ID: identity.TaskID, DomainID: item.ID, TaskType: "certificate_issue", Status: "running", Stage: "certificate_issue", OperatorID: "manager"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateAttempt(t.Context(), repository.TaskAttempt{ID: identity.AttemptID, TaskID: identity.TaskID, AttemptNumber: 1, Status: "running", Stage: "certificate_issue"}); err != nil {
		t.Fatal(err)
	}
	lease, err := f.members.AcquireDNSExecutionLease(t.Context(), item, identity)
	if err != nil {
		t.Fatal(err)
	}
	// 任务已终态也不能越过尚未清理的租约。
	if _, err := f.db.ExecContext(t.Context(), `UPDATE tasks SET status = 'failed' WHERE id = ?`, identity.TaskID); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":%q,"certificate_type":%q,"dns_provider":%q,"challenge_type":"dns-01"}`, item.Name, item.CertificateType, item.DNSProvider)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		response := serveUserRequest(f.server, f.cookies["manager"], method, "/api/v1/domains/"+item.ID, body)
		if response.Code != http.StatusConflict {
			t.Fatal("活动租约应返回冲突而非内部错误", method, response.Code, response.Body.String())
		}
		for _, secret := range []string{lease.ID, account.CredentialRef, "credential_ref"} {
			if secret != "" && strings.Contains(response.Body.String(), secret) {
				t.Fatal("冲突响应泄露租约或凭据引用")
			}
		}
	}
	if _, err := repository.NewSQLDomainRepository(f.db).GetByID(t.Context(), item.ID); err != nil {
		t.Fatal("冲突请求不能删除域名", err)
	}
}
