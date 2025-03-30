package oneparallel

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Job defines the interface for a job that can be run concurrently.
// It is defined as a subset of the standard library's os/exec.Cmd interface.
type Job interface {
	fmt.Stringer
	StdoutPipe() (io.ReadCloser, error)
	StderrPipe() (io.ReadCloser, error)
	// Start initiates the job and returns an error if it fails to start.
	Start() error
	// Stop stops the job and returns an error if it fails to stop.
	Stop() error
	// Wait blocks until the job completes and returns an error if it fails.
	Wait() error
}

// ExecJob is a job that wraps an [exec.Cmd].
type ExecJob struct {
	exec.Cmd
	customstr string
	siginted  bool
}

var _ Job = (*ExecJob)(nil)

// NewExecJob creates a new ExecJob.
func NewExecJob(cmd *exec.Cmd) *ExecJob {
	return &ExecJob{Cmd: *cmd}
}

// NewExecJobs creates a new ExecJob for each command.
func NewExecJobs(cmds ...*exec.Cmd) []*ExecJob {
	jobs := make([]*ExecJob, len(cmds))
	for i, cmd := range cmds {
		jobs[i] = NewExecJob(cmd)
	}
	return jobs
}

// String returns a string representation of the job.
func (j *ExecJob) String() string {
	if j.customstr != "" {
		return j.customstr
	}
	return QuotedArgs(j.Cmd.Args)
}

// QuotedArgs returns a string representation of the arguments, each quoted if
// needed. It is similar to [strconv.Quote].
func QuotedArgs(args []string) string {
	var b strings.Builder
	for i, arg := range args {
		quoted := strconv.Quote(arg)
		if arg == "" || strings.ContainsAny(arg, " ") || strings.Trim(quoted, "\"") != arg {
			arg = quoted
		}
		b.WriteString(arg)
		if i != len(args)-1 {
			b.WriteString(" ")
		}
	}
	return b.String()
}

// SetCustomString sets a custom string representation for the job.
func (j *ExecJob) SetCustomString(s string) {
	j.customstr = s
}

// Stop stops the job and returns an error if it fails to stop.
// It first tries to SIGINT the process, but if it's called again, it will
// SIGKILL the process.
func (j *ExecJob) Stop() error {
	if !j.siginted && runtime.GOOS != "windows" {
		slog.Debug(
			"sending SIGINT to command",
			"pid", j.Cmd.Process.Pid)

		j.siginted = true
		return j.Cmd.Process.Signal(os.Interrupt)
	}

	// out of patience!! or windows :(

	slog.Debug(
		"sending SIGKILL to command",
		"pid", j.Cmd.Process.Pid,
		"siginted", j.siginted)

	return j.Cmd.Process.Kill()
}
