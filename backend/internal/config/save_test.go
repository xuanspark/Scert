package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type failingConfigFile struct {
	*os.File
	stage string
	err   error
}

func (file failingConfigFile) Write(data []byte) (int, error) {
	if file.stage == "write" {
		return 0, file.err
	}
	if file.stage == "short_write" {
		return len(data) - 1, nil
	}
	return file.File.Write(data)
}

func (file failingConfigFile) Chmod(mode os.FileMode) error {
	if file.stage == "chmod" {
		return file.err
	}
	return file.File.Chmod(mode)
}

func (file failingConfigFile) Sync() error {
	if file.stage == "sync" {
		return file.err
	}
	return file.File.Sync()
}

func (file failingConfigFile) Close() error {
	err := file.File.Close()
	if file.stage == "close" {
		return file.err
	}
	return err
}

func TestSaveConfigFailuresPreserveDestination(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, stage := range []string{"create", "write", "short_write", "chmod", "ownership", "sync", "close", "replace"} {
			name := stage + "/new"
			if existing {
				name = stage + "/existing"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				directory := t.TempDir()
				path := filepath.Join(directory, "scert.json")
				original := []byte("original configuration\n")
				if existing {
					if err := os.WriteFile(path, original, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				injected := errors.New("injected save failure")
				operations := defaultConfigSaveOperations()
				operations.createTemp = func(dir, pattern string) (configTempFile, error) {
					if stage == "create" {
						return nil, injected
					}
					file, err := os.CreateTemp(dir, pattern)
					if err != nil {
						return nil, err
					}
					return failingConfigFile{File: file, stage: stage, err: injected}, nil
				}
				operations.ownership = func(string, string) error {
					if stage == "ownership" {
						return injected
					}
					return nil
				}
				replaceCalls := 0
				operations.replace = func(from, to string) error {
					replaceCalls++
					if stage == "replace" {
						return injected
					}
					return os.Rename(from, to)
				}
				expected := injected
				if stage == "short_write" {
					expected = io.ErrShortWrite
				}
				if err := saveConfigFile(path, []byte("new configuration\n"), operations); !errors.Is(err, expected) {
					t.Fatalf("预期保存失败且保留原因，实际：%v", err)
				}
				if stage != "replace" && replaceCalls != 0 {
					t.Fatal("准备阶段失败不得进入提交步骤")
				}
				data, err := os.ReadFile(path)
				if existing {
					if err != nil || string(data) != string(original) {
						t.Fatalf("失败保存不得改变旧配置：%q，%v", data, err)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("首次保存失败不得生成正式配置：%v", err)
				}
				assertNoConfigTemporaryFiles(t, directory)
			})
		}
	}
}

func TestSaveConfigPreparesBeforeReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "scert.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 历史固定名称临时文件不属于本次保存，必须原样保留。
	if err := os.WriteFile(path+".tmp", []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	operations := defaultConfigSaveOperations()
	prepared := false
	operations.ownership = func(dir, temporary string) error {
		if dir != directory || filepath.Dir(temporary) != directory || temporary == path || temporary == path+".tmp" {
			t.Fatal("权限准备只能作用于本次同目录随机临时文件")
		}
		prepared = true
		return nil
	}
	operations.replace = func(from, to string) error {
		if !prepared || to != path {
			t.Fatal("替换必须位于权限和归属准备之后")
		}
		old, err := os.ReadFile(path)
		if err != nil || string(old) != "old" {
			t.Fatal("提交前旧配置不得变化")
		}
		info, err := os.Stat(from)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("提交前临时文件权限不正确：%v", info.Mode())
		}
		return os.Rename(from, to)
	}
	if err := saveConfigFile(path, []byte("new"), operations); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("提交后正式内容不正确：%q，%v", data, err)
	}
	data, err = os.ReadFile(path + ".tmp")
	if err != nil || string(data) != "unrelated" {
		t.Fatal("不得覆盖或清理历史临时文件")
	}
	assertNoConfigTemporaryFiles(t, directory)
}

func TestSaveReplacesExistingConfiguration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "scert.json")
	cfg := Default(directory)
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	cfg.Server.Port = 9443
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Server.Port != 9443 {
		t.Fatalf("覆盖保存后配置读取不正确：%v", err)
	}
	assertNoConfigTemporaryFiles(t, directory)
}

func assertNoConfigTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".scert.json-") {
			t.Fatalf("本次保存遗留临时文件：%s", entry.Name())
		}
	}
}
