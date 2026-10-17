//go:build !unix

package agent

import "os/exec"

// configureProcess keeps exec's default cancellation (kill the child only).
func configureProcess(cmd *exec.Cmd) {}
