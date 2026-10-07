package main

import (
	"fmt"
	"os"

	"fvmx/internal/fvmx"
)

// version 在 release 构建时由 goreleaser 通过 -ldflags 注入
var version = "dev"

func main() {
	// 仅当 --version/-v 是第一个参数时才打印自身版本，
	// 否则 `fvmx flutter --version` 会被拦截而无法转发到 SDK
	if len(os.Args) > 1 {
		if arg := os.Args[1]; arg == "--version" || arg == "-v" {
			fmt.Println(version)
			return
		}
	}

	output, err := fvmx.Run(os.Args[1:], fvmx.Env{Version: version})
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
