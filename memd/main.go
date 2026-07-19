package main

import (
	"flag"
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	serve := flag.Bool("serve", false, "run the memory server on a Unix domain socket")
	flag.Parse()

	if *showVersion {
		fmt.Printf("splice-memd %s\n", version)
		os.Exit(0)
	}

	if *serve {
		runServer()
		os.Exit(0)
	}

	fmt.Fprintln(os.Stderr, "splice-memd: use --serve to start the memory server, or --version to print version")
	os.Exit(2)
}
