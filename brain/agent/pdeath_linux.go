package agent

import "syscall"

// dieWithParent makes the kernel kill the child when the brain dies, however it
// dies (a closed terminal tab, a crash): a long-running Claude would otherwise
// be left behind in its own process group.
func dieWithParent(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
