package httpchallenge

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
const testAuth = testToken + ".abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

func newTestLease(t *testing.T, s *Store, ctx context.Context, names ...string) *Lease {
	t.Helper()
	l, err := s.NewLease(ctx, names)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

func requestChallenge(s *Store, method, host, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, nil)
	r.Header.Set("X-Forwarded-Host", "a.example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestChallengeHostTokenAndMethodIsolation(t *testing.T) {
	s := NewStore()
	l := newTestLease(t, s, context.Background(), "a.example.com")
	if err := l.Present(context.Background(), "a.example.com", testToken, testAuth); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, host, path string
		status             int
	}{
		{"GET", "a.example.com", PathPrefix + testToken, 200},
		{"HEAD", "A.EXAMPLE.COM:80", PathPrefix + testToken, 200},
		{"GET", "b.example.com", PathPrefix + testToken, 404},
		{"POST", "a.example.com", PathPrefix + testToken, 404},
		{"GET", "a.example.com", PathPrefix + testToken + "x", 404},
		{"GET", "a.example.com", PathPrefix + testToken + "/", 404},
		{"GET", "a.example.com", PathPrefix + testToken + "?x=1", 404},
		{"GET", "a.example.com", PathPrefix + "%61" + testToken[1:], 404},
		{"GET", "a.example.com", "/unrelated/" + testToken, 404},
	} {
		w := requestChallenge(s, tc.method, tc.host, tc.path)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("挑战响应不正确：%+v，状态 %d", tc, w.Code)
		}
		if tc.status == 200 && tc.method == "GET" && w.Body.String() != testAuth {
			t.Fatal("挑战内容不匹配")
		}
		if (tc.status == 404 || tc.method == "HEAD") && w.Body.Len() != 0 {
			t.Fatal("拒绝响应或 HEAD 泄漏挑战内容")
		}
	}
}

func TestLeaseOwnershipCancellationAndExpiry(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := newTestLease(t, s, ctx, "a.example.com")
	second := newTestLease(t, s, context.Background(), "a.example.com")
	if err := first.Present(ctx, "a.example.com", testToken, testAuth); err != nil {
		t.Fatal(err)
	}
	if err := second.Present(ctx, "a.example.com", testToken, testAuth); err == nil {
		t.Fatal("不同订单不应覆盖同一挑战")
	}
	_ = second.CleanUp(ctx, "a.example.com", testToken, testAuth)
	if w := requestChallenge(s, "GET", "a.example.com", PathPrefix+testToken); w.Code != 200 {
		t.Fatal("其他订单清理删除了当前挑战")
	}
	cancel()
	if w := requestChallenge(s, "GET", "a.example.com", PathPrefix+testToken); w.Code != 404 {
		t.Fatal("已取消的挑战仍可读取")
	}
	if err := second.Present(context.Background(), "a.example.com", testToken, testAuth); err != nil {
		t.Fatal(err)
	}
	_ = first.CleanUp(ctx, "a.example.com", testToken, testAuth)
	first.Close()
	if w := requestChallenge(s, "GET", "a.example.com", PathPrefix+testToken); w.Code != 200 {
		t.Fatal("迟到清理误删了新订单挑战")
	}
	now = now.Add(s.ttl)
	if w := requestChallenge(s, "GET", "a.example.com", PathPrefix+testToken); w.Code != 404 {
		t.Fatal("到期挑战仍可读取")
	}
	second.Close()
	if err := second.Present(context.Background(), "a.example.com", testToken, testAuth); err == nil {
		t.Fatal("关闭后不应重新创建挑战")
	}
}

func TestChallengeValidationAndCapacity(t *testing.T) {
	s := NewStore()
	s.capacity = 1
	l := newTestLease(t, s, context.Background(), "a.example.com", "b.example.com")
	for _, tc := range []struct{ host, token, auth string }{
		{"c.example.com", testToken, testAuth},
		{"*.a.example.com", testToken, testAuth},
		{"a.example.com", "../token", testAuth},
		{"a.example.com", testToken, "other.auth"},
		{"a.example.com", testToken, testAuth + "\n"},
	} {
		if err := l.Present(context.Background(), tc.host, tc.token, tc.auth); err == nil {
			t.Fatal("无效挑战未被拒绝")
		}
	}
	if _, err := s.NewLease(context.Background(), []string{"*.example.com"}); err == nil {
		t.Fatal("泛域名订单未被拒绝")
	}
	if err := l.Present(context.Background(), "a.example.com", testToken, testAuth); err != nil {
		t.Fatal(err)
	}
	if err := l.Present(context.Background(), "b.example.com", testToken, testAuth); err == nil {
		t.Fatal("容量限制未生效")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = l.CleanUp(ctx, "a.example.com", testToken, testAuth)
	if err := l.Present(context.Background(), "b.example.com", testToken, testAuth); err != nil {
		t.Fatal("取消上下文阻碍清理：", err)
	}
}

func TestChallengeConcurrentOrders(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l, err := s.NewLease(ctx, []string{"a.example.com"})
			if err != nil {
				t.Error(err)
				return
			}
			defer l.Close()
			token := fmt.Sprintf("%s%d", testToken, i)
			auth := token + "." + strings.Repeat("a", 43)
			if err := l.Present(ctx, "a.example.com", token, auth); err != nil {
				t.Error(err)
				return
			}
			if w := requestChallenge(s, "GET", "a.example.com", PathPrefix+token); w.Code != 200 {
				t.Error("并发订单内容丢失")
			}
			cancel()
			_ = l.CleanUp(ctx, "a.example.com", token, auth)
		}(i)
	}
	wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) != 0 {
		t.Fatal("并发订单清理后仍有残留")
	}
}
