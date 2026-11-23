package main

import (
	"fmt"
	"os"

	"smem/internal/cli"
	"smem/internal/workspace"
)

func main() {
	layout, err := workspace.Resolve(workspace.ResolveOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "smem 初始化工作区失败：%v\n", err)
		os.Exit(1)
	}
	if err := cli.Run(os.Args[1:], layout, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}
