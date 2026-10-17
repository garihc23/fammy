//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the child in its own process group so cancellation
// also kills anything it spawned (e.g. stdio MCP servers).
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
