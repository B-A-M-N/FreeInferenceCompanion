package main

import (
	"fmt"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "-salt-test" {
		fmt.Fprintln(os.Stderr, "usage: salt-loader -salt-test <cache-dir>")
		os.Exit(1)
	}
	cacheDir := os.Args[2]
	os.Setenv("FI_CACHE_DIR", cacheDir)

	loader := runtime.DefaultSaltLoader()
	salt, err := loader()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%x\n", salt)
}
