package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

const maxSSHKeyRequestBytes = 2 * 1024 * 1024

type sshKeyRequest struct {
	Name       string `json:"name" binding:"required"`
	PrivateKey string `json:"private_key" binding:"required"`
	Passphrase string `json:"passphrase"`
}

func (s *Server) listSSHKeys(c *gin.Context) {
	if s.sshKeys == nil {
		s.writeError(c, http.StatusNotImplemented, "SSH 私钥管理接口尚未配置", errors.New("SSH 私钥管理服务未配置"))
		return
	}
	items, err := s.sshKeys.List(c.Request.Context())
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询 SSH 私钥失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createSSHKey(c *gin.Context) {
	if s.sshKeys == nil {
		s.writeError(c, http.StatusNotImplemented, "SSH 私钥管理接口尚未配置", errors.New("SSH 私钥管理服务未配置"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSSHKeyRequestBytes)
	var request sshKeyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(c, http.StatusRequestEntityTooLarge, "SSH 私钥请求内容过大", err)
			return
		}
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	item, err := s.sshKeys.Create(c.Request.Context(), request.Name, request.PrivateKey, request.Passphrase)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 私钥无效", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) deleteSSHKey(c *gin.Context) {
	if s.sshKeys == nil || s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "SSH 私钥管理接口尚未配置", errors.New("SSH 私钥或服务器管理服务未配置"))
		return
	}
	reference, err := s.sshKeys.Reference(c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 私钥 ID 无效", err)
		return
	}
	used, err := s.servers.IsPrivateKeyRefUsed(c.Request.Context(), reference)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "检查 SSH 私钥占用状态失败", err)
		return
	}
	if used {
		s.writeError(c, http.StatusConflict, "SSH 私钥仍被部署服务器使用", errors.New("SSH 私钥引用正在使用"))
		return
	}
	if err := s.sshKeys.Delete(c.Request.Context(), c.Param("id")); err != nil {
		s.writeError(c, http.StatusNotFound, "SSH 私钥不存在", err)
		return
	}
	c.Status(http.StatusNoContent)
}
