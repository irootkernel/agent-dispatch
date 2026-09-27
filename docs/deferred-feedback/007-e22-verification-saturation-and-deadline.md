# DF-007: Pin verification deadline and saturated-queue reporting

Recorded 2026-09-28 from the E22 whole-Epic review. The next owner is the
sync verification follow-up.

**Affected authority.** `internal/cli/sync_verify.go` owns the four-minute
command deadline and verification-job admission; `internal/adapters/sqlite/sync.go`
owns the shared unresolved-job queue. The relevant acceptance contract is
AC-1804 and the bounded-work requirement is SYN-015.

**Bounded concern.** The deadline is applied to the command context and is
shorter than interrupted-job expiry, but the current test pins only that
ordering. A future edit could remove the timeout without failing a test. A
group whose unresolved obligations saturate the shared queue cannot admit a
new verification job. The command then returns `sync_precondition_failed`
with category `conflict` at exit 14 instead of a durable `incomplete` verdict.
It does not report a false clean pair; `sync
status` still exposes the unresolved obligations and admission refusal. This
requires sustained degradation and is separate from the normal dirty,
pending, stale, or uncertain verification paths already tested.

**Reconsideration condition.** When verification admission, the shared queue,
or the command deadline next changes, add a stalling-transport test with a
short injected deadline and preserve the planned/expired record behavior.
Evaluate a reserved verification slot or separate observation capacity under
the queue limit, with a saturated-queue integration case and an explicit
operator-visible result. Do not allow queue pressure to produce a false
`complete` verdict.
