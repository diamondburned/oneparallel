package oneparallel

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
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
	stopped   atomic.Int32
}

var _ Job = (*ExecJob)(nil)

// NewExecJob creates a new ExecJob.
func NewExecJob(cmd *exec.Cmd) *ExecJob {
	j := &ExecJob{Cmd: *cmd}
	initExecJob(j)
	return j
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

// Start starts the job and returns an error if it fails to start.
func (j *ExecJob) Start() error {
	if j.stopped.Load() > 0 {
		return errors.New("job has already been stopped")
	}
	return j.Cmd.Start()
}

// Stop stops the job and returns an error if it fails to stop.
// It first tries to SIGINT the process, but if it's called again, it will
// SIGKILL the process.
//
// It is safe to call Stop concurrently.
func (j *ExecJob) Stop() error {
	return killExecJob(j, j.stopped.Add(1))
}
