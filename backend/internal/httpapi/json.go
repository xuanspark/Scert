package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// bindJSON ???? JSON ?????????????????? JSON?
func bindJSON(c *gin.Context, target any, limit int64) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("???????")
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
			return errors.New("????????? JSON ??")
		}
		return err
	}
	return nil
}
