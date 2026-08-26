package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type Server struct {
	config config.Config
	http   *http.Server
}

func New(cfg config.Config) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/api/v1/admin/status", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"service":       "scert-admin",
			"reset_enabled": false,
			"listen_scope":  "local_only",
			"message":       "密码重置协议尚未启用，当前仅提供受保护服务入口",
		})
	})
	return &Server{config: cfg, http: &http.Server{
		Addr:              net.JoinHostPort(cfg.Admin.ResetServiceHost, strconv.Itoa(cfg.Admin.ResetServicePort)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}}
}

func (s *Server) ListenAndServe() error {
	if s.config.Admin.ResetServiceHost != "127.0.0.1" && s.config.Admin.ResetServiceHost != "::1" {
		return fmt.Errorf("Scert-admin 仅允许监听本机回环地址")
	}
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok", "service": "scert-admin"})
}
