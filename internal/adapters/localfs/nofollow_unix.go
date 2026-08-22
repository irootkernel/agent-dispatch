//go:build unix

package localfs

import "syscall"

// syscall_NOFOLLOW opens the final path component without following a
// symlink at that component (PTH-002). Agent Dispatch targets macOS and Linux;
// both define O_NOFOLLOW.
const syscall_NOFOLLOW = syscall.O_NOFOLLOW
