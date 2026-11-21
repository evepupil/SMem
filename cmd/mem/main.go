package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"smem/internal/cli"
	"smem/internal/workspace"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "mem:", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	layout, err := workspace.Discover(home, cwd)
	if err != nil {
		return err
	}
	return cli.Run(os.Args[1:], layout, os.Stdout, os.Stderr)
}
