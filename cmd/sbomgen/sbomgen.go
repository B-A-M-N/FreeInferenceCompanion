// Command sbomgen generates a minimal SPDX JSON SBOM of the module
// dependencies for the FreeInference Companion release. It reads the
// dependency graph from `go list -m -json all` and writes an SPDX-2.3
// document.
//
// Usage: go run ./cmd/sbomgen <version> <output-path>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type pkg struct {
	SPDXID           string              `json:"SPDXID"`
	Name             string              `json:"name"`
	Version          string              `json:"versionInfo"`
	Download         string              `json:"downloadLocation"`
	FilesAnalyzed    bool                `json:"filesAnalyzed"`
	Checksums        []map[string]string `json:"checksums,omitempty"`
	LicenseConcluded string              `json:"licenseConcluded"`
	LicenseDeclared  string              `json:"licenseDeclared"`
	Copyright        string              `json:"copyrightText"`
}

type doc struct {
	SpdxVersion       string `json:"spdxVersion"`
	DataLicense       string `json:"dataLicense"`
	SPDXID            string `json:"SPDXID"`
	Name              string `json:"name"`
	DocumentNamespace string `json:"documentNamespace"`
	CreationInfo      struct {
		Created  string   `json:"created"`
		Creators []string `json:"creators"`
	} `json:"creationInfo"`
	Packages []pkg `json:"packages"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sbomgen <version> <output-path>")
		os.Exit(1)
	}
	version := os.Args[1]
	outPath := os.Args[2]

	list := exec.Command("go", "list", "-m", "-json", "all")
	list.Dir = workDir()
	data, err := list.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go list: %v\n", err)
		os.Exit(1)
	}

	var packages []pkg
	idx := 0
	for _, block := range strings.Split(string(data), "}\n{") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if !strings.HasPrefix(block, "{") {
			block = "{" + block
		}
		if !strings.HasSuffix(block, "}") {
			block = block + "}"
		}
		var m struct {
			Path    string
			Version string
			Hash    string
		}
		if err := json.Unmarshal([]byte(block), &m); err != nil {
			continue
		}
		if m.Path == "" || m.Version == "" {
			continue
		}
		idx++
		p := pkg{
			SPDXID:           fmt.Sprintf("SPDXRef-Package-%d", idx),
			Name:             m.Path,
			Version:          m.Version,
			Download:         "https://" + m.Path,
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			Copyright:        "NOASSERTION",
		}
		if m.Hash != "" {
			p.Checksums = []map[string]string{{"algorithm": "SHA256", "checksumValue": m.Hash}}
		}
		packages = append(packages, p)
	}

	d := doc{
		SpdxVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              "freeinference-companion-" + version,
		DocumentNamespace: "https://github.com/b-a-m-n/freeinference-companion/releases/" + version,
	}
	d.CreationInfo.Created = time.Now().UTC().Format(time.RFC3339)
	d.CreationInfo.Creators = []string{"Tool: freeinference-companion-makefile"}
	d.Packages = packages

	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}

// workDir returns the repository root where the go.mod file is located.
func workDir() string {
	wd, _ := os.Getwd()
	return wd
}
