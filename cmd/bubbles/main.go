package main

import (
	"os"

	"github.com/pauluszhou/bubbles/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
