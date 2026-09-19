package releasearchive

import (
	"bytes"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Verify 检查构建标记及本机构建路径，不将被禁止的路径写入发布清单。
func Verify(source string, binaries, forbiddenPaths []string) error {
	if source == "" || len(binaries) == 0 || len(forbiddenPaths) == 0 {
		return errors.New("发布检查必须提供源目录、二进制文件和本机路径")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("检查发布源目录失败：%w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("发布源路径必须是普通目录")
	}
	patterns := pathPatterns(forbiddenPaths)
	if len(patterns) == 0 {
		return errors.New("本机路径不能为空")
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("发布检查不接受符号链接或特殊文件")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := scanPaths(file, patterns); err != nil {
			relative, _ := filepath.Rel(source, path)
			return fmt.Errorf("发布文件 %s 检查失败：%w", relative, err)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, name := range binaries {
		if !filepath.IsLocal(name) {
			return errors.New("二进制文件必须位于发布目录内")
		}
		info, err := buildinfo.ReadFile(filepath.Join(source, name))
		if err != nil {
			return fmt.Errorf("读取 %s 的 Go 构建信息失败：%w", name, err)
		}
		trimmed := false
		for _, setting := range info.Settings {
			if setting.Key == "-trimpath" && setting.Value == "true" {
				trimmed = true
			}
		}
		if !trimmed {
			return fmt.Errorf("二进制文件 %s 未启用 -trimpath", name)
		}
	}
	return nil
}

func pathPatterns(paths []string) [][]byte {
	var patterns [][]byte
	seen := make(map[string]bool)
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		slash := strings.ReplaceAll(path, `\`, "/")
		backslash := strings.ReplaceAll(slash, "/", `\`)
		for _, variant := range []string{slash, backslash, strings.ReplaceAll(backslash, `\`, `\\`), strings.ReplaceAll(slash, "/", `\/`)} {
			utf8Pattern := []byte(variant)
			lowerASCII(utf8Pattern)
			if seen[string(utf8Pattern)] {
				continue
			}
			seen[string(utf8Pattern)] = true
			patterns = append(patterns, utf8Pattern)
			var little, big []byte
			for _, unit := range utf16.Encode([]rune(variant)) {
				little = append(little, byte(unit), byte(unit>>8))
				big = append(big, byte(unit>>8), byte(unit))
			}
			lowerASCII(little)
			lowerASCII(big)
			patterns = append(patterns, little, big)
		}
	}
	return patterns
}

func scanPaths(reader io.Reader, patterns [][]byte) error {
	longest := 0
	for _, pattern := range patterns {
		if len(pattern) > longest {
			longest = len(pattern)
		}
	}
	if longest == 0 {
		return errors.New("路径检查模式不能为空")
	}
	const chunkSize = 64 * 1024
	buffer := make([]byte, chunkSize+longest-1)
	retained := 0
	for {
		n, err := reader.Read(buffer[retained:])
		data := buffer[:retained+n]
		lowerASCII(data)
		for _, pattern := range patterns {
			if bytes.Contains(data, pattern) {
				return errors.New("发现本机构建路径，拒绝发布")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// 保留跨读取边界的前缀，避免路径被分块后漏检。
		retained = min(longest-1, len(data))
		copy(buffer, data[len(data)-retained:])
	}
}

func lowerASCII(data []byte) {
	for i, value := range data {
		if value >= 'A' && value <= 'Z' {
			data[i] = value + ('a' - 'A')
		}
	}
}
