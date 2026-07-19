package main

import (
	"os"

	"github.com/Taf0711/splice/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
