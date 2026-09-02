// Command sbomgen generates a minimal SPDX 2.3 JSON SBOM of the module
// dependencies for the FreeInference Companion release. It streams
// `go list -m -json all` using json.Decoder (which handles concatenated JSON
// correctly), includes the root application package, models SPDX
// relationships (DESCRIBES + DEPENDS_ON), and uses NOASSERTION for any
// download location it cannot verify.
//
// Usage: go run ./cmd/sbomgen <version> <output-path>
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
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

type relationship struct {
	SPDXID        string `json:"spdxElementId"`
	RelatedSPDXID string `json:"relatedSpdxElement"`
	Relationship  string `json:"relationshipType"`
}

type doc struct {
	SpdxVersion       string         `json:"spdxVersion"`
	DataLicense       string         `json:"dataLicense"`
	SPDXID            string         `json:"SPDXID"`
	DocName           string         `json:"name"`
	DocumentNamespace string         `json:"documentNamespace"`
	CreationInfo      creationInfo   `json:"creationInfo"`
	Packages          []pkg          `json:"packages"`
	Relationships     []relationship `json:"relationships"`
}

type creationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sbomgen <version> <output-path>")
		os.Exit(1)
	}
	version := strings.TrimPrefix(os.Args[1], "v")
	outPath := os.Args[2]

	list := exec.Command("go", "list", "-m", "-json", "all")
	list.Dir = workDir()
	data, err := list.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go list: %v\n", err)
		os.Exit(1)
	}

	mod := module{Path: "github.com/b-a-m-n/freeinference-companion", Version: version}
	var deps []module
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for dec.More() {
		var m struct {
			Path    string
			Version string
			Hash    string
			Main    bool
		}
		if err := dec.Decode(&m); err != nil {
			fmt.Fprintf(os.Stderr, "decode module: %v\n", err)
			os.Exit(1)
		}
		if m.Path == "" || m.Version == "" {
			continue
		}
		if m.Main {
			mod.Version = m.Version
			mod.Hash = m.Hash
			continue
		}
		deps = append(deps, module{Path: m.Path, Version: m.Version, Hash: m.Hash})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })

	packages := []pkg{makePackage("SPDXRef-Package-Root", mod)}
	rels := []relationship{
		{SPDXID: "SPDXRef-DOCUMENT", RelatedSPDXID: "SPDXRef-Package-Root", Relationship: "DESCRIBES"},
	}
	for i, d := range deps {
		id := fmt.Sprintf("SPDXRef-Package-%d", i+1)
		packages = append(packages, makePackage(id, d))
		rels = append(rels,
			relationship{SPDXID: "SPDXRef-Package-Root", RelatedSPDXID: id, Relationship: "DEPENDS_ON"},
		)
	}

	d := doc{
		SpdxVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		DocName:           "freeinference-companion-" + version,
		DocumentNamespace: "https://github.com/b-a-m-n/freeinference-companion/releases/" + version,
		CreationInfo: creationInfo{
			Created:  time.Now().UTC().Format(time.RFC3339),
			Creators: []string{"Tool: freeinference-companion-makefile"},
		},
		Packages:      packages,
		Relationships: rels,
	}

	tmp := outPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		f.Close()
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "close: %v\n", err)
		os.Exit(1)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "rename: %v\n", err)
		os.Exit(1)
	}
}

type module struct {
	Path    string
	Version string
	Hash    string
}

func makePackage(spdxID string, m module) pkg {
	p := pkg{
		SPDXID:           spdxID,
		Name:             m.Path,
		Version:          m.Version,
		Download:         "NOASSERTION",
		FilesAnalyzed:    false,
		LicenseConcluded: "NOASSERTION",
		LicenseDeclared:  "NOASSERTION",
		Copyright:        "NOASSERTION",
	}
	// Go module hashes are base64-encoded SHA-256 h1 hashes prefixed with
	// "h1:". SPDX expects the digest as hexadecimal SHA-256, so decode the
	// base64 bytes and emit those bytes as hex. If decoding fails, omit.
	if m.Hash != "" {
		if algo, hexsum, ok := decodeGoHash(m.Hash); ok {
			p.Checksums = []map[string]string{{"algorithm": algo, "checksumValue": hexsum}}
		}
	}
	return p
}

func decodeGoHash(h string) (algo, hexsum string, ok bool) {
	const prefix = "h1:"
	if !strings.HasPrefix(h, prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(h[len(prefix):])
	if err != nil {
		return "", "", false
	}
	// Go's h1 hash is sha256 of the module zip. Emit as hex.
	hexsum = fmt.Sprintf("%x", raw)
	if len(hexsum) != 64 {
		return "", "", false
	}
	return "SHA256", hexsum, true
}

// workDir returns the repository root where the go.mod file is located.
func workDir() string {
	wd, _ := os.Getwd()
	return wd
}
