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
// by E1-T4; v2 drops the over-constraining attempts uniqueness (E4-T5
// gate finding); v3 records the accepting target scope (E4 audit); v4
// adds the work-receipt begin windows (E5 audit); v5 persists the
// observation position (E7-T8); v6 adds the batch-sequence watermark
// (E8-T1).
const SchemaRange = "1-6"

// AdapterVersions returns the pinned adapter implementation versions the
// build carries. Each entry names the delivered surface and its verified
// interface baseline instead of inventing a version that does not exist.
func AdapterVersions() map[string]string {
	return map[string]string{
		"watchman":      "bounded parser and managed trigger lifecycle, verified watchman 2026.07.27.00 (E2)",
		"localfs":       "containment resolver (E2-T2)",
		"sqlite":        "schema-v1..v6 (v2 attempts uniqueness, v3 target scope, v4 work-receipt begin windows, v5 observation position, v6 batch sequence watermark)",
		"hermeskanban":  "public-cli transport, capability probe, and durable sink, verified hermes 0.19.1 (E4-T1..T4)",
		"hermeswebhook": "static-declaration HTTPS sink, transport acceptance only, from the E0-T4 s9 evidence (E6-T1)",
	}
}
