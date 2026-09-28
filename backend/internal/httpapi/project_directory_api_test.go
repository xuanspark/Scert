package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type projectDirectoryBoundary struct {
	repository.ProjectDirectoryRepository
	before func()
	err    error
}

func (r projectDirectoryBoundary) ListVisibleProjects(ctx context.Context, actor, after string, size int) (repository.VisibleProjectPage, error) {
	if r.before != nil {
		r.before()
	}
	if r.err != nil {
		return repository.VisibleProjectPage{}, r.err
	}
	return r.ProjectDirectoryRepository.ListVisibleProjects(ctx, actor, after, size)
}

func TestProjectDirectoryAPI(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for _, tc := range []struct {
		actor         string
		status, count int
		manage        bool
	}{
		{"", 401, 0, false}, {"primary-super-admin", 200, 3, true},
		{"manager", 200, 1, true}, {"viewer", 200, 1, false}, {"global-admin", 200, 0, false},
	} {
		t.Run("visibility_"+tc.actor, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects?page_size=5", nil)
			if tc.actor != "" {
				req.AddCookie(f.cookies[tc.actor])
			}
			response := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(response, req)
			if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
				t.Fatal("项目响应没有禁止共享缓存")
			}
			if response.Code != tc.status {
				t.Fatalf("项目入口响应错误：%d，%s", response.Code, response.Body.String())
			}
			if tc.status != 200 {
				return
			}
			var page repository.VisibleProjectPage
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Items == nil || len(page.Items) != tc.count {
				t.Fatalf("项目响应不正确：%s，%v", response.Body.String(), err)
			}
			for _, item := range page.Items {
				if item.CanManageMembers != tc.manage {
					t.Fatalf("前端能力提示错误：%+v", item)
				}
			}
			for _, field := range []string{"password", "email", "username", "project_members"} {
				if strings.Contains(response.Body.String(), field) {
					t.Fatalf("项目入口包含无关隐私：%s", field)
				}
			}
		})
	}
	for _, query := range []string{"page_size=7", "page_size=5&page_size=10", "after=x&after=y", "actor=primary-super-admin", "after=%00", "page_size=", "page_size=5;after=x"} {
		t.Run("invalid_"+query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects?"+query, nil)
			req.AddCookie(f.cookies["manager"])
			response := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(response, req)
			if response.Code != 400 {
				t.Fatalf("项目入口接受未知或无效输入：%d", response.Code)
			}
		})
	}
	for _, size := range []string{"5", "10", "15", "30"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects?page_size="+size+"&after=zzzz", nil)
		req.AddCookie(f.cookies["manager"])
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, req)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"items":[]`) {
			t.Fatalf("支持页长或空页失败：%s", response.Body.String())
		}
	}
}

func TestProjectDirectoryAPIBoundaries(t *testing.T) {
	for _, mode := range []string{"missing_repository", "storage_error", "disabled_after_session", "revoked_after_session"} {
		t.Run(mode, func(t *testing.T) {
			f := newProjectMemberAPIFixture(t)
			status := 200
			switch mode {
			case "missing_repository":
				f.server.projectDirectory = nil
				status = 503
			case "storage_error":
				f.server.projectDirectory = projectDirectoryBoundary{err: errors.New("secret database detail")}
				status = 500
			case "disabled_after_session", "revoked_after_session":
				if mode == "disabled_after_session" {
					status = 403
				}
				f.server.projectDirectory = projectDirectoryBoundary{ProjectDirectoryRepository: f.members, before: func() {
					query := "DELETE FROM project_members WHERE user_id='manager'"
					if mode == "disabled_after_session" {
						query = "UPDATE users SET status='disabled' WHERE id='manager'"
					}
					if _, err := f.db.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}}
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
			req.AddCookie(f.cookies["manager"])
			response := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(response, req)
			if response.Code != status || strings.Contains(response.Body.String(), "secret database detail") {
				t.Fatalf("项目入口错误边界失效：%d，%s", response.Code, response.Body.String())
			}
			if mode == "revoked_after_session" && !strings.Contains(response.Body.String(), `"items":[]`) {
				t.Fatal("会话验证后撤权仍然可见项目")
			}
		})
	}
}

func TestProjectMemberAPIPrivateCache(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for _, actor := range []string{"", "manager", "viewer"} {
		req := httptest.NewRequest(http.MethodGet, memberTestPath, nil)
		if actor != "" {
			req.AddCookie(f.cookies[actor])
		}
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, req)
		if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
			t.Fatalf("成员响应允许共享缓存：%s", actor)
		}
	}
}
