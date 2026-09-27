package database

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"testing"
	"testing/fstest"
)

func checkMigrationCatalog(source, embedded fs.FS) error {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		directory := "migrations/" + driver
		for _, catalog := range []fs.FS{source, embedded} {
			entries, err := fs.ReadDir(catalog, directory)
			if err != nil {
				return err
			}
			if len(entries) != latestMigrationVersion {
				return fmt.Errorf("%s 的文件数 %d 与登记版本 %d 不一致", directory, len(entries), latestMigrationVersion)
			}
			for version, entry := range entries {
				expected := fmt.Sprintf("%04d_%s.sql", version+1, migrationName(version+1))
				if entry.IsDir() || entry.Name() != expected {
					return fmt.Errorf("%s 的迁移文件应为 %s，实际为 %s", directory, expected, entry.Name())
				}
			}
		}
		for version := 1; version <= latestMigrationVersion; version++ {
			name := path.Join(directory, fmt.Sprintf("%04d_%s.sql", version, migrationName(version)))
			content, err := fs.ReadFile(source, name)
			if err != nil {
				return err
			}
			packed, err := fs.ReadFile(embedded, name)
			if err != nil {
				return err
			}
			if len(bytes.TrimSpace(content)) == 0 || !bytes.Equal(content, packed) {
				return fmt.Errorf("%s 为空或与嵌入内容不一致", name)
			}
		}
	}
	return nil
}

func TestMigrationCatalogMatchesSourceAndVersionRegistry(t *testing.T) {
	if err := checkMigrationCatalog(os.DirFS("."), migrationFiles); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationCatalogDetectsIncompleteChanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(fstest.MapFS, fstest.MapFS)
	}{
		{"unregistered_source", func(source, embedded fstest.MapFS) {
			source["migrations/mysql/0010_extra.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		}},
		{"missing_embed", func(source, embedded fstest.MapFS) { delete(embedded, "migrations/postgres/0001_initial.sql") }},
		{"missing_source", func(source, embedded fstest.MapFS) { delete(source, "migrations/sqlite/0001_initial.sql") }},
		{"changed_embed", func(source, embedded fstest.MapFS) {
			embedded["migrations/mysql/0001_initial.sql"] = &fstest.MapFile{Data: []byte("SELECT 2;")}
		}},
		{"empty_file", func(source, embedded fstest.MapFS) {
			source["migrations/sqlite/0001_initial.sql"] = &fstest.MapFile{Data: []byte(" \n")}
		}},
		{"wrong_name", func(source, embedded fstest.MapFS) {
			source["migrations/mysql/0001_wrong.sql"] = source["migrations/mysql/0001_initial.sql"]
			delete(source, "migrations/mysql/0001_initial.sql")
		}},
		{"unregistered_embed", func(source, embedded fstest.MapFS) {
			embedded["migrations/postgres/0010_extra.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, embedded := fstest.MapFS{}, fstest.MapFS{}
			for _, driver := range []string{"sqlite", "mysql", "postgres"} {
				for version := 1; version <= latestMigrationVersion; version++ {
					name := fmt.Sprintf("migrations/%s/%04d_%s.sql", driver, version, migrationName(version))
					source[name] = &fstest.MapFile{Data: []byte("SELECT 1;")}
					embedded[name] = &fstest.MapFile{Data: []byte("SELECT 1;")}
				}
			}
			if err := checkMigrationCatalog(source, embedded); err != nil {
				t.Fatalf("测试基线无效：%v", err)
			}
			tc.mutate(source, embedded)
			if err := checkMigrationCatalog(source, embedded); err == nil {
				t.Fatal("未检出不完整的迁移变更")
			}
		})
	}
}
