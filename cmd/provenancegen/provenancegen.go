// Command provenancegen generates an unsigned in-toto provenance statement
// envelope describing the FreeInference Companion release build. It scans the
// release directory for all artifacts and lists each as a subject with its
// SHA-256 digest.
//
// Usage: go run ./cmd/provenancegen <version> <commit> <release-dir> <output-path>
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: provenancegen <version> <commit> <release-dir> <output-path>")
		os.Exit(1)
	}
	version := os.Args[1]
	commit := os.Args[2]
	releaseDir := os.Args[3]
	outPath := os.Args[4]

	// Collect all release artifacts with their SHA-256 digests.
	var subjects []map[string]any
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read release dir: %v\n", err)
		os.Exit(1)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(releaseDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", e.Name(), err)
			os.Exit(1)
		}
		sum := sha256.Sum256(data)
		subjects = append(subjects, map[string]any{
			"name": e.Name(),
			"digest": map[string]string{
				"sha256": hex.EncodeToString(sum[:]),
			},
		})
	}
	sort.Slice(subjects, func(i, j int) bool {
		return subjects[i]["name"].(string) < subjects[j]["name"].(string)
	})

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
		"subject":       subjects,
		"predicateType": "https://slsa.dev/provenance/v0.2",
		"predicate": map[string]any{
			"buildType": "https://github.com/b-a-m-n/freeinference-companion/Makefile@v1",
			"invocation": map[string]any{
				"configSource": map[string]any{
					"uri": "git+https://github.com/b-a-m-n/freeinference-companion@" + version,
					"digest": map[string]string{
						"gitCommit": commit,
					},
					"entryPoint": "make release",
				},
				"parameters": map[string]any{
					"version": version,
					"commit":  commit,
				},
				"environment": map[string]any{
					"go-version": gv,
					"os":         goos,
					"arch":       goarch,
				},
			},
			"metadata": map[string]any{
				"buildInvocationId": "make-release-" + version,
				"buildStartedOn":    time.Now().UTC().Format(time.RFC3339),
				"completeness": map[string]any{
					"parameters":  true,
					"environment": false,
					"materials":   false,
				},
				"reproducible": false, // Build environment (Go version, OS, compiler flags) not fully captured in materials; full reproducibility requires recording all build-tool versions and environment variables.
			},
			"materials": []map[string]any{{
				"uri": "git+https://github.com/b-a-m-n/freeinference-companion@" + commit,
				"digest": map[string]string{
					"gitCommit": commit,
				},
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
