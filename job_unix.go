package oneparallel

import (
	"log/slog"
	"syscall"
)

func initExecJob(j *ExecJob) {
	// Force use of process group for better cleanup.
	j.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func killExecJob(j *ExecJob, stopped int32) error {
	signal := syscall.SIGKILL
	if stopped == 1 {
		// Try to gracefully interrupt first.
		signal = syscall.SIGINT
	}

	slog.Debug(
		"sending signal to process group",
		"pid", j.Cmd.Process.Pid,
		"signal", signal.String(),
		"stopped", stopped)

	return syscall.Kill(-j.Cmd.Process.Pid, signal)
}
