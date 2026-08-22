// Package secretresolver resolves configuration secret references
// (configuration-spec §11, SEC-006) immediately before use: the env,
// file, fd, and (on darwin) keychain forms turn one parsed reference
// into its value at the single point of use. The config loader stores
// references only; the resolved value never enters SQLite, logs, or
// command output, and callers must redact it from every diagnostic
// (SEC-007).
package secretresolver
