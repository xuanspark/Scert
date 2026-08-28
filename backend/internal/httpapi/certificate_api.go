package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const certificateUploadLimit = 20 << 20

type certificateReader interface {
	Current(context.Context, string) (certificate.IssueResult, error)
	ListVersions(context.Context, string) ([]certificate.SavedResource, error)
}

type certificatePathRequest struct {
	CertificatePath  string `json:"certificate_path"`
	PrivateKeyPath   string `json:"private_key_path"`
	IssuerBundlePath string `json:"issuer_bundle_path"`
}

func (s *Server) listCertificates(c *gin.Context) {
	if s.repository == nil || s.currentCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书管理接口尚未配置", errors.New("证书服务未配置"))
		return
	}
	items, err := s.repository.List(c.Request.Context())
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询域名证书失败", err)
		return
	}
	result := make([]gin.H, 0, len(items))
	for _, item := range items {
		entry := gin.H{"domain": item, "certificate": nil}
		current, currentErr := s.currentCertificate.Current(c.Request.Context(), item.ID)
		if currentErr == nil {
			entry["certificate"] = current
		} else {
			entry["certificate_error"] = currentErr.Error()
		}
		result = append(result, entry)
	}
	c.JSON(http.StatusOK, gin.H{"items": result})
}

func (s *Server) getCertificate(c *gin.Context) {
	if s.currentCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书管理接口尚未配置", errors.New("证书服务未配置"))
		return
	}
	result, err := s.currentCertificate.Current(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		s.writeError(c, certificateErrorStatus(err), "读取当前证书失败", err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) listCertificateVersions(c *gin.Context) {
	if s.currentCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书管理接口尚未配置", errors.New("证书服务未配置"))
		return
	}
	items, err := s.currentCertificate.ListVersions(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		s.writeError(c, certificateErrorStatus(err), "查询证书历史版本失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) validateCertificate(c *gin.Context) {
	if s.currentCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书校验接口尚未配置", errors.New("证书服务未配置"))
		return
	}
	result, err := s.currentCertificate.Current(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		s.writeError(c, certificateErrorStatus(err), "读取当前证书失败", err)
		return
	}
	validation, err := (certificate.LocalValidator{VerifyChain: true}).ValidateFiles(
		c.Request.Context(),
		result.Saved.CertificatePath,
		result.Saved.FullChainPath,
		result.Saved.PrivateKeyPath,
		result.Domains,
	)
	if err != nil {
		s.writeError(c, http.StatusUnprocessableEntity, "证书校验失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"certificate": result, "validation": validation})
}

func (s *Server) deployCertificateCurrent(c *gin.Context) {
	if s.deployCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书部署接口尚未配置", errors.New("证书部署流程未配置"))
		return
	}
	result, report, err := s.deployCertificate(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	response := gin.H{"certificate": result, "deployment": report}
	if err != nil {
		response["error"] = err.Error()
		c.JSON(http.StatusBadGateway, response)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (s *Server) importCertificateFiles(c *gin.Context) {
	if s.importCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书导入接口尚未配置", errors.New("证书导入流程未配置"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, certificateUploadLimit)
	request, cleanup, err := parseCertificateImportRequest(c)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "证书上传参数无效", err)
		return
	}
	defer cleanup()
	result, report, err := s.importCertificate(c.Request.Context(), request)
	response := gin.H{"certificate": result, "deployment": report}
	if err != nil {
		response["error"] = err.Error()
		c.JSON(http.StatusBadGateway, response)
		return
	}
	c.JSON(http.StatusOK, response)
}

func parseCertificateImportRequest(c *gin.Context) (certificate.ImportRequest, func(), error) {
	cleanup := func() {}
	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(certificateUploadLimit); err != nil {
			return certificate.ImportRequest{}, cleanup, fmt.Errorf("解析上传表单失败：%w", err)
		}
		certificatePath, cleanCertificate, err := saveMultipartFile(c.Request.MultipartForm, "certificate")
		if err != nil {
			return certificate.ImportRequest{}, cleanup, err
		}
		privateKeyPath, cleanPrivateKey, err := saveMultipartFile(c.Request.MultipartForm, "private_key")
		if err != nil {
			cleanCertificate()
			return certificate.ImportRequest{}, cleanup, err
		}
		issuerPath, cleanIssuer, err := saveOptionalMultipartFile(c.Request.MultipartForm, "issuer_bundle")
		if err != nil {
			cleanCertificate()
			cleanPrivateKey()
			return certificate.ImportRequest{}, cleanup, err
		}
		cleanup = func() {
			cleanCertificate()
			cleanPrivateKey()
			cleanIssuer()
		}
		return certificate.ImportRequest{
			DomainID: c.Param("id"), Certificate: certificatePath, PrivateKey: privateKeyPath, IssuerBundle: issuerPath,
		}, cleanup, nil
	}
	var body certificatePathRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		return certificate.ImportRequest{}, cleanup, fmt.Errorf("读取证书路径失败：%w", err)
	}
	if strings.TrimSpace(body.CertificatePath) == "" {
		return certificate.ImportRequest{}, cleanup, errors.New("证书路径不能为空")
	}
	if strings.TrimSpace(body.PrivateKeyPath) == "" {
		return certificate.ImportRequest{}, cleanup, errors.New("私钥路径不能为空")
	}
	return certificate.ImportRequest{
		DomainID: c.Param("id"), Certificate: body.CertificatePath, PrivateKey: body.PrivateKeyPath, IssuerBundle: body.IssuerBundlePath,
	}, cleanup, nil
}

func saveMultipartFile(form *multipart.Form, field string) (string, func(), error) {
	path, cleanup, err := saveOptionalMultipartFile(form, field)
	if err != nil {
		return "", cleanup, err
	}
	if path == "" {
		return "", cleanup, fmt.Errorf("缺少上传文件：%s", field)
	}
	return path, cleanup, nil
}

func saveOptionalMultipartFile(form *multipart.Form, field string) (string, func(), error) {
	cleanup := func() {}
	if form == nil {
		return "", cleanup, errors.New("上传表单为空")
	}
	files := form.File[field]
	if len(files) == 0 {
		return "", cleanup, nil
	}
	file, err := files[0].Open()
	if err != nil {
		return "", cleanup, fmt.Errorf("打开上传文件失败：%w", err)
	}
	defer file.Close()
	temporary, err := os.CreateTemp("", "scert-upload-*")
	if err != nil {
		return "", cleanup, fmt.Errorf("创建上传临时文件失败：%w", err)
	}
	path := temporary.Name()
	cleanup = func() { _ = os.Remove(path) }
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("设置上传临时文件权限失败：%w", err)
	}
	written, err := io.CopyN(temporary, file, certificateUploadLimit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		_ = temporary.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("保存上传文件失败：%w", err)
	}
	if written > certificateUploadLimit {
		_ = temporary.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("上传文件超过 %d MB 限制", certificateUploadLimit>>20)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("关闭上传临时文件失败：%w", err)
	}
	return path, cleanup, nil
}

func certificateErrorStatus(err error) int {
	if repository.IsNotFound(err) || errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusUnprocessableEntity
}
