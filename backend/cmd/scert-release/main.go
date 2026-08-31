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
	var executables stringList
	source := flag.String("source", "", "发布源目录")
	output := flag.String("output", "", "发布归档路径")
	format := flag.String("format", "", "归档格式：tar.gz 或 zip")
	flag.Var(&executables, "executable", "需要写入 0755 权限的相对路径，可重复指定")
	flag.Parse()

	if err := releasearchive.Create(releasearchive.Options{
		Source: *source, Destination: *output, Format: releasearchive.Format(*format), Executables: executables,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "创建发布归档失败："+err.Error())
		os.Exit(1)
	}
}
