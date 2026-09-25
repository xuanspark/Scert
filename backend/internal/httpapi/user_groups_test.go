package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestUserGroupVisibilityAndWriteProtection(t *testing.T) {
	server, users, _ := protectedUserServer(t)
	password := "Member123a"
	hash := httpUserPasswordHash(t, password)
	for _, item := range []repository.User{
		{ID: "leader", Username: "leader", Role: "admin", Groups: []string{"甲组", "乙组"}},
		{ID: "peer", Username: "peer", Role: "admin", Groups: []string{"乙组", "隐藏组"}},
		{ID: "first", Username: "first", Role: "user", Groups: []string{"甲组"}},
		{ID: "second", Username: "second", Role: "user", Groups: []string{"乙组"}},
		{ID: "other", Username: "other", Role: "user", Groups: []string{"隐藏组"}},
		{ID: "default-user", Username: "default-user", Role: "user"},
		{ID: "default-admin", Username: "default-admin", Role: "admin"},
	} {
		item.PasswordHash, item.Status = hash, "active"
		if err := users.CreateUser(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"leader", []string{"first", "leader", "peer", "second"}},
		{"first", []string{"first"}},
		{"default-admin", []string{"default-admin", "default-user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cookie := loginAuthTest(t, server, tc.name, password, false).Result().Cookies()[0]
			response := serveUserRequest(server, cookie, http.MethodGet, "/api/v1/users", "")
			if response.Code != http.StatusOK {
				t.Fatalf("读取分组失败：%d %s", response.Code, response.Body.String())
			}
			var body struct {
				Items []repository.User `json:"items"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(body.Items))
			for _, item := range body.Items {
				ids = append(ids, item.ID)
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("用户范围错误：%v", ids)
			}
			if strings.Contains(response.Body.String(), "隐藏组") {
				t.Fatal("泄露组外分组名称")
			}
			if tc.name == "first" && strings.Contains(response.Body.String(), `"groups"`) {
				t.Fatal("普通用户不应收到分组信息")
			}
			outside := serveUserRequest(server, cookie, http.MethodGet, "/api/v1/users/other", "")
			if outside.Code != http.StatusNotFound {
				t.Fatalf("组外读取应返回 404：%d", outside.Code)
			}
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
				path := "/api/v1/users/first"
				if method == http.MethodPost {
					path = "/api/v1/users"
				}
				result := serveUserRequest(server, cookie, method, path, `{"username":"first","role":"admin","groups":["隐藏组"]}`)
				if result.Code != http.StatusForbidden {
					t.Fatalf("越权写入应返回 403：%s %d", method, result.Code)
				}
			}
		})
	}
	superCookie := loginAuthTest(t, server, "admin", "AdminPass123", false).Result().Cookies()[0]
	all := serveUserRequest(server, superCookie, http.MethodGet, "/api/v1/users", "")
	if all.Code != http.StatusOK || !strings.Contains(all.Body.String(), "隐藏组") || !strings.Contains(all.Body.String(), "default-user") {
		t.Fatal("超级管理员应读取所有分组")
	}
	for _, payload := range []string{
		`{"username":"new-user","password":"Member123a","role":"user","groups":["甲组","乙组"]}`,
		`{"username":"new-user","password":"Member123a","role":"user","groups":["默认分组"]}`,
		`{"username":"new-user","password":"Member123a","role":"user","groups":[""]}`,
	} {
		response := serveUserRequest(server, superCookie, http.MethodPost, "/api/v1/users", payload)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法分组未拒绝：%d", response.Code)
		}
	}
	// 降级时不得静默丢弃管理员的多组成员关系。
	badRole := serveUserRequest(server, superCookie, http.MethodPut, "/api/v1/users/leader", `{"username":"leader","role":"user"}`)
	if badRole.Code != http.StatusBadRequest {
		t.Fatal("多组管理员降级应要求明确选择单组")
	}
	updated := serveUserRequest(server, superCookie, http.MethodPut, "/api/v1/users/leader", `{"username":"leader","role":"user","groups":["乙组"]}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("单组降级失败：%s", updated.Body.String())
	}
	cleared := serveUserRequest(server, superCookie, http.MethodPut, "/api/v1/users/leader", `{"username":"leader","groups":[]}`)
	if cleared.Code != http.StatusOK {
		t.Fatalf("回到默认分组失败：%s", cleared.Body.String())
	}
	item, err := users.GetUserByID(t.Context(), "leader")
	if err != nil || len(item.Groups) != 0 {
		t.Fatalf("分组清空未保存：%v", err)
	}
}

func TestUserGroupsAreReadFromCurrentRepository(t *testing.T) {
	server, users, _ := protectedUserServer(t)
	hash := httpUserPasswordHash(t, "Member123a")
	for _, item := range []repository.User{
		{ID: "leader", Username: "leader", Role: "admin", Groups: []string{"甲组"}},
		{ID: "first", Username: "first", Role: "user", Groups: []string{"甲组"}},
		{ID: "second", Username: "second", Role: "user", Groups: []string{"乙组"}},
	} {
		item.PasswordHash, item.Status = hash, "active"
		if err := users.CreateUser(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginAuthTest(t, server, "leader", "Member123a", false).Result().Cookies()[0]
	item, _ := users.GetUserByID(t.Context(), "leader")
	item.Groups = []string{"乙组"}
	if err := users.UpdateUser(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	response := serveUserRequest(server, cookie, http.MethodGet, "/api/v1/users", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"id":"first"`) || !strings.Contains(response.Body.String(), `"id":"second"`) {
		t.Fatalf("旧会话未应用新分组范围：%s", response.Body.String())
	}
}
