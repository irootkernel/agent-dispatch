// Package version exposes build metadata reported by the version command.
//
// Values are injected at build time via -ldflags; the defaults describe a
// build from an untagged working tree.
package version

// Version is the semantic version of the binary.
var Version = "0.1.0-dev"

// Commit is the source revision the binary was built from.
var Commit = "unknown"

// BuildTime is the RFC 3339 timestamp of the build.
var BuildTime = "unknown"

// ConfigVersion is the operator configuration version this build supports.
const ConfigVersion = "1"

// SchemaRange is the range of SQLite migration versions this build
// supports, inclusive; schema v1 is the initial durable schema delivered
// by E1-T4.
const SchemaRange = "1-1"

// AdapterVersions returns the pinned adapter implementation versions the
// build carries. E1-T1 ships no adapters, so the map names each planned
// adapter with its "not yet implemented" marker instead of inventing a
// version that does not exist.
func AdapterVersions() map[string]string {
	return map[string]string{
		"watchman":      "not-implemented (E2)",
		"localfs":       "not-implemented (E2)",
		"sqlite":        "not-implemented (E1-T4)",
		"hermeskanban":  "not-implemented (E4)",
		"hermeswebhook": "not-implemented (E6)",
	}
}
