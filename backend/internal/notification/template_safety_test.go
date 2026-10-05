package notification

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	htmlparser "golang.org/x/net/html"
)

func TestFailedTaskCannotHaveCompletedSummary(t *testing.T) {
	for _, status := range []string{"failed", "error", "unknown"} {
		t.Run(status, func(t *testing.T) {
			payload, err := New(newTestConfig("http://mail.example.test"), nil).buildPayload(Event{
				Status:     status,
				Domain:     "example.test",
				Deployment: deployment.Report{Total: 2, Success: 2, Status: "success"},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{payload.Text, payload.HTML} {
				if strings.Contains(body, "已完成") {
					t.Fatal("失败或未知任务不能显示完成摘要")
				}
			}
		})
	}
}

func TestRecoveryWarningSurvivesHiddenDetails(t *testing.T) {
	for _, report := range []deployment.Report{
		{RecoveryStatus: "unconfirmed"},
		{RecoveryStatus: "failed"},
		{RecoveryStatus: "recovery_required"},
		{Results: []deployment.Result{{Status: "failed", Message: "已恢复旧文件，但服务或站点恢复状态未确认，已保留部署锁"}}},
	} {
		cfg := newTestConfig("http://mail.example.test")
		cfg.IncludeDeploymentDetails = false
		payload, err := New(cfg, nil).buildPayload(Event{Status: "failed", Domain: "example.test", Deployment: report})
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{payload.Text, payload.HTML} {
			if !strings.Contains(body, "勿直接重试或删除锁") {
				t.Fatal("隐藏部署明细不能隐藏恢复安全提示")
			}
		}
	}
}

func TestRecoveryWarningUsesFailureCodes(t *testing.T) {
	for _, code := range []deployment.FailureCode{
		deployment.FailureRollback, deployment.FailureRuntimeUnknown,
		deployment.FailureRecoveryPending, deployment.FailureRecoveryUnknown,
		deployment.FailureLock, deployment.FailureLockRelease,
	} {
		t.Run(string(code), func(t *testing.T) {
			cfg := newTestConfig("http://mail.example.test")
			cfg.IncludeDeploymentDetails = false
			payload, err := New(cfg, nil).buildPayload(Event{
				Status: "failed", Domain: "example.test", Error: "primary failure",
				Deployment: deployment.Report{Results: []deployment.Result{
					{Status: "failed", Message: "first failure"},
					{Status: "failed", Message: "second failure"},
					{Server: "hidden-server", Status: "failed", FailureCode: code},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{payload.Text, payload.HTML} {
				if !strings.Contains(body, "勿直接重试或删除锁") || strings.Contains(body, "hidden-server") {
					t.Fatal("恢复警告必须独立展示且不能暴露已隐藏的服务器名称")
				}
			}
		})
	}
}

func TestVerifiedRecoveryDoesNotRequireManualUnlock(t *testing.T) {
	event := Event{Status: "failed", Deployment: deployment.Report{
		RecoveryStatus: "success",
		Results:        []deployment.Result{{FailureCode: deployment.FailureRecoveryRestored}},
	}}
	if recoveryWarning(event) != "" {
		t.Fatal("已确认恢复的任务不应提示恢复未确认")
	}
	event = Event{Status: "failed", Error: "已保留部署锁"}
	if recoveryWarning(event) == "" {
		t.Fatal("仅任务错误携带恢复信息时也应提示核查")
	}
}

func TestMailUsesCertificateDomainInsteadOfInternalID(t *testing.T) {
	for _, domains := range [][]string{{" ", "example.test"}, nil} {
		payload, err := New(newTestConfig("http://mail.example.test"), nil).buildPayload(Event{
			Status: "success", DomainID: "internal-domain-id", Domains: domains,
		})
		if err != nil {
			t.Fatal(err)
		}
		expected := "未指定域名"
		if len(domains) > 0 {
			expected = "example.test"
		}
		for _, body := range []string{payload.Subject, payload.Text, payload.HTML} {
			if strings.Contains(body, "internal-domain-id") || !strings.Contains(body, expected) {
				t.Fatal("用户邮件应显示证书域名而非内部记录编号")
			}
		}
	}
}

func TestAllMailTemplatesHaveValidBodyStyles(t *testing.T) {
	for _, eventType := range []string{"certificate_issue", "certificate_renewal", "management_renewal", "certificate_import", "unknown"} {
		for _, status := range []string{"success", "failed", "error"} {
			payload, err := New(newTestConfig("http://mail.example.test"), nil).buildPayload(Event{
				Type: eventType, Status: status, Domain: "example.test",
			})
			if err != nil {
				t.Fatal(err)
			}
			assertMailBodyStyle(t, payload.HTML)
		}
	}
	assertMailBodyStyle(t, buildSecurityHTML("01234567"))
	if strings.Contains(buildSecurityHTML(`<script>alert(1)</script>`), "<script>") {
		t.Fatal("模板必须转义动态内容")
	}
}

func assertMailBodyStyle(t *testing.T, body string) {
	t.Helper()
	tokenizer := htmlparser.NewTokenizer(strings.NewReader(body))
	for {
		switch tokenizer.Next() {
		case htmlparser.ErrorToken:
			if tokenizer.Err() != io.EOF {
				t.Fatal(tokenizer.Err())
			}
			t.Fatal("邮件缺少有效正文样式")
		case htmlparser.StartTagToken:
			token := tokenizer.Token()
			if token.Data != "body" {
				continue
			}
			if len(token.Attr) != 1 || token.Attr[0].Key != "style" ||
				!strings.Contains(token.Attr[0].Val, "line-height:1.6;") {
				t.Fatal("正文样式属性被引号截断或缺失")
			}
			return
		}
	}
}

func TestSecurityMailUsesStyledTemplate(t *testing.T) {
	var received mailPayload
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	cfg := newTestConfig(endpoint.URL)
	cfg.Recipients = []string{"unrelated@example.test"}
	if err := New(cfg, nil).SendSecurityCode(t.Context(), "bound@example.test", "01234567"); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"border-radius", "01234567", "两分钟", "请勿提供此验证码"} {
		if !strings.Contains(received.HTML, expected) {
			t.Fatalf("验证码邮件缺少样式或安全信息：%s", expected)
		}
	}
	if len(received.To) != 1 || received.To[0].Email != "bound@example.test" || len(received.CC) != 0 || len(received.BCC) != 0 {
		t.Fatal("验证码只能发送到绑定邮箱")
	}
}
