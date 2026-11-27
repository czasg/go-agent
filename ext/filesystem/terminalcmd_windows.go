//go:build windows

package filesystem

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func terminalCmd(ctx context.Context, raw, dir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /c chcp 65001 >nul & ` + raw}
	cmd.Dir = dir
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd
}
