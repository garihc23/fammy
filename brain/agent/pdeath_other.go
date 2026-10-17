//go:build unix && !linux

package agent

import "syscall"

// dieWithParent: no parent-death signal here; the brain closes its sessions on exit.
func dieWithParent(a *syscall.SysProcAttr) {}
