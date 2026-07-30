//go:build saltloader

package main

import (
	"encoding/hex"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

func init() {
	// Register the salt-test handler for external test scripts.
	// Only compiled when the saltloader build tag is set.
	if len(os.Args) > 1 && os.Args[1] == "-salt-test" {
		if len(os.Args) < 3 {
			os.Stderr.WriteString("usage: fi -salt-test <cache-dir>\n")
			os.Exit(1)
		}
		os.Setenv("FI_CACHE_DIR", os.Args[2])
		loader := runtime.DefaultSaltLoader()
		salt, err := loader()
		if err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString(hex.EncodeToString(salt) + "\n")
		os.Exit(0)
	}
}
