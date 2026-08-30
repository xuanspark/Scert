package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// bindJSON 严格解析 JSON 请求，并拒绝未知字段和多段 JSON。
func bindJSON(c *gin.Context, target any, limit int64) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("请求上下文或请求体为空")
	}
	if limit > 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("请求体包含多段 JSON 数据")
		}
		return err
	}
	return nil
}
