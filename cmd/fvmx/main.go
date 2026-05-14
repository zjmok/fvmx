package main

import (
	"fmt"
	"os"

	"fvmx/internal/fvmx"
)

func main() {
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
