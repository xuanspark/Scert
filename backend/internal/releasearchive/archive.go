package releasearchive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Format string

const (
	FormatTarGz Format = "tar.gz"
	FormatZip   Format = "zip"
)

type Options struct {
	Source      string
	Destination string
	Format      Format
	Executables []string
}

type entry struct {
	path     string
	relative string
	info     fs.FileInfo
}

var archiveTime = time.Unix(0, 0).UTC()

func Create(options Options) error {
	_, destination, executables, entries, err := prepare(options)
	if err != nil {
		return err
	}
	if err := ensureExecutableEntries(executables, entries); err != nil {
		return err
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("发布归档已存在，拒绝覆盖：%s", destination)
		}
		return fmt.Errorf("创建发布归档失败：%w", err)
	}
	completed := false
	defer func() {
		_ = file.Close()
		if !completed {
			_ = os.Remove(destination)
		}
	}()

	switch options.Format {
	case FormatTarGz:
		err = writeTarGz(file, entries, executables)
	case FormatZip:
		err = writeZip(file, entries, executables)
	default:
		err = fmt.Errorf("不支持的发布归档格式：%s", options.Format)
	}
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步发布归档失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭发布归档失败：%w", err)
	}
	completed = true
	return nil
}

func prepare(options Options) (string, string, map[string]struct{}, []entry, error) {
	if options.Source == "" || options.Destination == "" {
		return "", "", nil, nil, errors.New("发布源目录和归档路径不能为空")
	}
	source, err := filepath.Abs(options.Source)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("解析发布源目录失败：%w", err)
	}
	destination, err := filepath.Abs(options.Destination)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("解析发布归档路径失败：%w", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("检查发布源目录失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", nil, nil, errors.New("发布源路径必须是普通目录")
	}
	if inside(source, destination) {
		return "", "", nil, nil, errors.New("发布归档不能位于发布源目录内")
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", "", nil, nil, fmt.Errorf("发布归档已存在，拒绝覆盖：%s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", nil, nil, fmt.Errorf("检查发布归档失败：%w", err)
	}

	executables := make(map[string]struct{}, len(options.Executables))
	for _, name := range options.Executables {
		normalized, err := normalizeRelative(name)
		if err != nil {
			return "", "", nil, nil, fmt.Errorf("可执行文件路径无效：%w", err)
		}
		executables[normalized] = struct{}{}
	}

	var entries []entry
	err = filepath.WalkDir(source, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("发布目录不能包含符号链接：%s", path)
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("发布目录包含不支持的文件类型：%s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{path: path, relative: filepath.ToSlash(relative), info: info})
		return nil
	})
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("检查发布目录失败：%w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relative < entries[j].relative })
	return source, destination, executables, entries, nil
}

func ensureExecutableEntries(executables map[string]struct{}, entries []entry) error {
	for executable := range executables {
		found := false
		for _, item := range entries {
			if item.relative == executable && item.info.Mode().IsRegular() {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("发布目录缺少可执行文件：%s", executable)
		}
	}
	return nil
}

func writeTarGz(output io.Writer, entries []entry, executables map[string]struct{}) error {
	gzipWriter := gzip.NewWriter(output)
	gzipWriter.Header.ModTime = archiveTime
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)

	for _, item := range entries {
		header, err := tar.FileInfoHeader(item.info, "")
		if err != nil {
			return fmt.Errorf("创建 tar 文件头失败：%w", err)
		}
		header.Name = item.relative
		header.Uid = 0
		header.Gid = 0
		header.Uname = "root"
		header.Gname = "root"
		header.ModTime = archiveTime
		header.AccessTime = archiveTime
		header.ChangeTime = archiveTime
		header.Mode = archiveMode(item, executables)
		if item.info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("写入 tar 文件头失败：%w", err)
		}
		if item.info.Mode().IsRegular() {
			if err := copyEntry(tarWriter, item.path); err != nil {
				return err
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("关闭 tar 归档失败：%w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("关闭 gzip 归档失败：%w", err)
	}
	return nil
}

func writeZip(output io.Writer, entries []entry, executables map[string]struct{}) error {
	zipWriter := zip.NewWriter(output)
	for _, item := range entries {
		header, err := zip.FileInfoHeader(item.info)
		if err != nil {
			return fmt.Errorf("创建 zip 文件头失败：%w", err)
		}
		header.Name = item.relative
		header.Modified = archiveTime
		header.SetMode(fs.FileMode(archiveMode(item, executables)))
		if item.info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}
		if item.info.Mode().IsRegular() {
			header.Method = zip.Deflate
		}
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("写入 zip 文件头失败：%w", err)
		}
		if item.info.Mode().IsRegular() {
			if err := copyEntry(writer, item.path); err != nil {
				return err
			}
		}
	}
	if err := zipWriter.Close(); err != nil {
		return fmt.Errorf("关闭 zip 归档失败：%w", err)
	}
	return nil
}

func copyEntry(destination io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开发布文件失败：%w", err)
	}
	defer file.Close()
	if _, err := io.Copy(destination, file); err != nil {
		return fmt.Errorf("写入发布文件失败：%w", err)
	}
	return nil
}

func archiveMode(item entry, executables map[string]struct{}) int64 {
	if item.info.IsDir() {
		return 0o755
	}
	if _, ok := executables[item.relative]; ok {
		return 0o755
	}
	return 0o644
}

func normalizeRelative(path string) (string, error) {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == "" || strings.HasPrefix(path, "/") || path == ".." || strings.HasPrefix(path, "../") {
		return "", errors.New("路径必须位于发布目录内")
	}
	return path, nil
}

func inside(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
