//go:build !windows

package filesystem

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

func terminalCmd(ctx context.Context, raw, dir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", raw)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd
}
