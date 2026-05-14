package main

import (
	"fmt"
	"os"

	"fvmx/internal/fvmx"
)

// version 在 release 构建时由 goreleaser 通过 -ldflags 注入
var version = "dev"

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Println(version)
			return
		}
	}

	output, err := fvmx.Run(os.Args[1:], fvmx.Env{})
	if err != nil {
		if exitErr, ok := err.(*fvmx.ExitError); ok {
			if exitErr.Message != "" {
				fmt.Fprintln(os.Stderr, exitErr.Message)
			}
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if output != "" {
		fmt.Println(output)
	}
}
