// Package version holds the build version, set at link time with
// -ldflags "-X github.com/zakiego/lumbung/apps/engine/internal/version.Version=...".
package version

// Version is the engine and CLI version. "dev" for local builds.
var Version = "dev"
