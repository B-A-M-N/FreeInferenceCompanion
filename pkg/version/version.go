// Package version is the single source of truth for the FreeInference
// Companion version. Both the CLI and the adapters import this package, so
// there is no import cycle and no way for the versions to diverge.
//
// The release version is injected at build time via -ldflags:
//
//	go build -ldflags "-X github.com/b-a-m-n/freeinference-companion/version.Version=0.2.0"
//
// The fallback matches the plugin manifest versions for development builds.
package version

// Version is the semantic version of the FreeInference Companion. It is
// stamped onto CLI output, adapter snapshots, and plugin manifests. main
// overrides this via -ldflags for release builds; the fallback keeps
// development builds self-consistent.
var Version = "0.1.0"
