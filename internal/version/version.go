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
// (E8-T1); v7 adds the record revision columns (E9-T1); v8 adds the
// resource observation revision (E10-T1); v9 persists the managed
// Watch bindings (E10-T2); v10 records the destinations-contract
// cutover marker (E11-T1); v11 binds the capability fingerprint to
// route activation (E11-T2); v12 adds the aggregate fan-out record
// families (E12-T1); v13 re-keys coordination onto destination lanes
// (E12-T2); v14 widens the work-receipt outcome vocabulary (E12-T3);
// v15 records each merged batch's destination-selection evidence
// (E12 epic validation); v16 creates the durable notification outbox of
// notification events and attempts (E13-T1, ADR-0019); v17 creates the
// disabled-route baseline records of ADR-0020 (E14-T2, DUR-017); v18
// persists the serialization-group topology of ADR-0021 (E15-T1).
const SchemaRange = "1-19"

// AdapterVersions returns the pinned adapter implementation versions the
// build carries. Each entry names the delivered surface and its verified
// interface baseline instead of inventing a version that does not exist.
func AdapterVersions() map[string]string {
	return map[string]string{
		"watchman":      "bounded parser and managed trigger lifecycle, verified watchman 2026.07.27.00 (E2)",
		"localfs":       "containment resolver (E2-T2)",
		"sqlite":        "schema-v1..v19 (v2 attempts uniqueness through v7 record revision columns, v8-v9 observation revision and managed watch bindings, v10-v11 destination cutover and capability binding, v12-v13 aggregate fan-out on destination lanes, v14-v15 receipt outcomes and batch evidence, v16 durable notification outbox, v17 disabled-route baselines, v18 serialization-group topology, v19 notification drain leases and drain-run evidence)",
		"hermeskanban":  "public-cli transport, capability probe, and durable sink, verified hermes 0.20.5 (E15-T4)",
		"hermeswebhook": "static-declaration HTTPS sink, transport acceptance only, from the E0-T4 s9 evidence (E6-T1)",
	}
}
