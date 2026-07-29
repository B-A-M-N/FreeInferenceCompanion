// Command provenancegen generates an in-toto attestation-style provenance
// file describing the FreeInference Companion release build.
//
// Usage: go run ./cmd/provenancegen <version> <commit> <output-path>
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: provenancegen <version> <commit> <output-path>")
		os.Exit(1)
	}
	version := os.Args[1]
	commit := os.Args[2]
	outPath := os.Args[3]

	goVerOut, _ := exec.Command("go", "version").Output()
	kv := strings.Split(strings.TrimSpace(string(goVerOut)), " ")
	gv := ""
	if len(kv) >= 3 {
		gv = kv[2]
	}

	envOut, _ := exec.Command("go", "env", "GOOS", "GOARCH").Output()
	lines := strings.Split(strings.TrimSpace(string(envOut)), "\n")
	goos, goarch := "", ""
	if len(lines) >= 2 {
		goos = lines[0]
		goarch = lines[1]
	}

	statement := map[string]any{
		"subject": []map[string]any{{
			"name":   "github.com/b-a-m-n/freeinference-companion",
			"digest": map[string]any{"gitCommit": commit},
		}},
		"predicateType": "https://slsa.dev/provenance/v0.2",
		"predicate": map[string]any{
			"buildType": "https://freeinference-companion.dev/provenance/v1",
			"invocation": map[string]any{
				"configSource": map[string]any{
					"uri": "git+https://github.com/b-a-m-n/freeinference-companion@" + version,
				},
				"parameters": map[string]any{"version": version, "commit": commit},
				"environment": map[string]any{
					"go-version": gv,
					"os":         goos,
					"arch":       goarch,
				},
			},
			"metadata": map[string]any{
				"buildInvocationId": "make-release-" + version,
				"completeness": map[string]any{
					"parameters":  true,
					"environment": false,
					"materials":   false,
				},
				"reproducible": false,
			},
			"materials": []map[string]any{{
				"uri":    "git+https://github.com/b-a-m-n/freeinference-companion",
				"digest": map[string]any{"gitCommit": commit},
			}},
		},
	}

	payload, err := json.Marshal(statement)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}

	envelope := map[string]any{
		"payloadType": "application/vnd.in-toto+json",
		"payload":     base64.StdEncoding.EncodeToString(payload),
	}

	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(envelope); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}
