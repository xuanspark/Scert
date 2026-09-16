package bootstrap

import "encoding/base64"

func encodeToken(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
