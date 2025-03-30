package oneparallel

import (
	"log/slog"
	"syscall"
)

func initExecJob(j *ExecJob) {
	// https://stackoverflow.com/a/66500411/5041327
	j.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killExecJob(j *ExecJob, stopped int32) error {
	slog.Debug(
		"killing process directly",
		"pid", j.Cmd.Process.Pid,
		"stopped", stopped)

	return j.Process.Kill()
}
