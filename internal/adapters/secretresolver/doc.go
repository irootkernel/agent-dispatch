// Package secretresolver resolves configuration secret references
// (configuration-spec §11, SEC-006) immediately before use: the env,
// file, and fd forms turn one parsed reference into its value at the
// single point of use on every supported platform. The keychain form
// is darwin-only; on Linux and other non-darwin builds a keychain
// reference fails closed as a typed UnresolvedError (never a panic).
// The config loader stores references only; the resolved value never
// enters SQLite, logs, or command output, and callers must redact it
// from every diagnostic (SEC-007).
package secretresolver
