package app

import "os"

var osMkdirAll = func(path string) error {
	return os.MkdirAll(path, 0o750)
}
