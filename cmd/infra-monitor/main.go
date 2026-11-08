package main

import (
	"os"

	"github.com/xonoko/infra-monitor/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
