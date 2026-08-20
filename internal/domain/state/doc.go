// Package state encodes the dispatch and route runtime state machines as
// validated transition services (E3-T1, persistence-and-state-machines §3
// and §6). The tables are pure and deterministic (TST-001): every allowed
// and forbidden transition is declared once here, guarded edges demand the
// typed evidence the architecture requires, and the acceptance and
// execution axes stay separate so acceptance never implies execution
// success. The SQLite adapter delegates its transition validation to this
// package so exactly one table is authoritative (DUR-011).
package state
