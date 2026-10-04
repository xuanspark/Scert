package adminconsole

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type testTerminalInput struct {
	values   []string
	hidden   []bool
	prompts  []string
	shown    []string
	failAt   int
	readHook func(int)
}

func (s *testTerminalInput) Read(ctx context.Context, prompt string, hidden bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.hidden = append(s.hidden, hidden)
	s.prompts = append(s.prompts, prompt)
	index := len(s.hidden)
	if s.readHook != nil {
		s.readHook(index)
	}
	if s.failAt == index {
		return "", errors.New("test-input-failure")
	}
	if index > len(s.values) {
		return "", errors.New("test-input-exhausted")
	}
	return s.values[index-1], nil
}
func (s *testTerminalInput) Show(message string) error {
	s.shown = append(s.shown, message)
	return nil
}

type testResetClient struct {
	methods  []localreset.Method
	calls    []string
	fail     string
	password string
}

func (c *testResetClient) Methods() []localreset.Method { return c.methods }
func (c *testResetClient) invoke(action string) error {
	c.calls = append(c.calls, action)
	if c.fail == action {
		return localreset.ErrProtocol
	}
	return nil
}
func (c *testResetClient) Select(value string) error {
	if value != "email" {
		return localreset.ErrProtocol
	}
	return c.invoke("select")
}
func (c *testResetClient) Verify(value string) error {
	if value != "01234567" {
		return localreset.ErrProtocol
	}
	return c.invoke("verify")
}
func (c *testResetClient) Reset(value string) error {
	c.password = value
	return c.invoke("reset")
}
func (*testResetClient) Close() error { return nil }

func TestAdminConsoleFlowRequiresVerificationAndHiddenInput(t *testing.T) {
	client := &testResetClient{methods: []localreset.Method{{ID: "email"}}}
	screen := &testTerminalInput{values: []string{"1", "01234567", "NewPass123!", "NewPass123!"}}
	if err := resetFlow(t.Context(), client, screen); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.calls, []string{"select", "verify", "reset"}) || client.password != "NewPass123!" {
		t.Fatal("重置未按验证后提交的顺序执行")
	}
	if !reflect.DeepEqual(screen.hidden, []bool{false, true, true, true}) {
		t.Fatal("验证码或密码出现回显")
	}
	for _, prompt := range screen.prompts {
		if strings.Contains(prompt, "01234567") || strings.Contains(prompt, "NewPass123!") {
			t.Fatal("提示文字泄露秘密")
		}
	}
}

func TestAdminConsoleFailureNeverSubmitsPassword(t *testing.T) {
	for _, scenario := range []string{"unknown-method", "cancel-choice", "select", "verify", "input-code", "input-password", "input-confirm", "weak-password", "mismatch", "cancel-after-verify"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &testResetClient{methods: []localreset.Method{{ID: "email"}}}
			screen := &testTerminalInput{values: []string{"1", "01234567", "NewPass123!", "NewPass123!"}}
			switch scenario {
			case "unknown-method":
				client.methods[0].ID = "unknown"
			case "cancel-choice":
				screen.values[0] = "0"
			case "select", "verify":
				client.fail = scenario
			case "input-code":
				screen.failAt = 2
			case "input-password":
				screen.failAt = 3
			case "input-confirm":
				screen.failAt = 4
			case "weak-password":
				screen.values[2] = "short"
			case "mismatch":
				screen.values[3] = "WrongPass12!"
			case "cancel-after-verify":
				screen.readHook = func(index int) {
					if index == 4 {
						cancel()
					}
				}
			}
			if err := resetFlow(ctx, client, screen); err == nil {
				t.Fatal("失败条件未被拒绝")
			}
			for _, call := range client.calls {
				if call == "reset" {
					t.Fatal("验证或交互失败仍然提交了密码")
				}
			}
		})
	}
}

func TestAdminConsoleShowsUnavailableResetCause(t *testing.T) {
	screen := &testTerminalInput{}
	showResetFailure(screen, localreset.ErrUnavailable)
	if len(screen.shown) != 1 || !strings.Contains(screen.shown[0], `验证码发送服务或本机重置通道不可用`) {
		t.Fatalf(`未显示SMTP或本机通道不可用提示：%v`, screen.shown)
	}
}

func TestAdminConsoleRejectsAllArgumentBasedInput(t *testing.T) {
	for _, args := range [][]string{
		{"--password", "TestPass123!"}, {"--config", "scert.json"}, {"--code-output", "reset-code"}, {"TestPass123!"}, {"--help"},
	} {
		err := Run(t.Context(), args)
		if err == nil {
			t.Fatal("接受了命令行参数")
		}
		if strings.Contains(err.Error(), "TestPass123!") {
			t.Fatal("错误消息泄露了参数中的密码")
		}
	}
}
