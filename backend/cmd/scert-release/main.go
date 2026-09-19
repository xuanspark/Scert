package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/releasearchive"
)

type stringList []string

func (items *stringList) String() string {
	return strings.Join(*items, ",")
}

func (items *stringList) Set(value string) error {
	*items = append(*items, value)
	return nil
}

func main() {
	var executables, binaries, forbiddenPaths stringList
	source := flag.String("source", "", "发布源目录")
	output := flag.String("output", "", "发布归档路径")
	format := flag.String("format", "", "归档格式：tar.gz 或 zip")
	verifyOnly := flag.Bool("verify-only", false, "仅检查路径裁剪和本机路径残留")
	flag.Var(&executables, "executable", "需要写入 0755 权限的相对路径，可重复指定")
	flag.Var(&binaries, "binary", "需要检查路径裁剪的二进制相对路径，可重复指定")
	flag.Var(&forbiddenPaths, "forbid-path", "不得写入产物的本机构建路径，可重复指定")
	flag.Parse()

	if *verifyOnly {
		if err := releasearchive.Verify(*source, binaries, forbiddenPaths); err != nil {
			fmt.Fprintln(os.Stderr, "发布路径检查失败："+err.Error())
			os.Exit(1)
		}
		fmt.Println("发布路径检查通过")
		return
	}

	if err := releasearchive.Create(releasearchive.Options{
		Source: *source, Destination: *output, Format: releasearchive.Format(*format), Executables: executables,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "创建发布归档失败："+err.Error())
		os.Exit(1)
	}
}
