package oneparallel

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// JobLimiter is a simple semaphore implementation that can be used to limit
// the number of concurrent jobs running at any given time.
//
// A zero value [JobLimiter] will not limit the number of concurrent jobs.
type JobLimiter struct {
	sema chan struct{}
}

// NewJobLimiter creates a new [JobLimiter].
func NewJobLimiter(limit int) JobLimiter {
	if limit <= 0 {
		return JobLimiter{}
	}
	return JobLimiter{
		sema: make(chan struct{}, limit),
	}
}

// Acquire acquires a slot in the semaphore. If the limit has been reached,
// this will block until a slot becomes available.
func (j JobLimiter) Acquire(ctx context.Context) (release func(), err error) {
	if j.sema == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		} else {
			return func() {}, nil
		}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case j.sema <- struct{}{}:
		return func() { <-j.sema }, nil
	}
}

// JobRunners is a collection of [JobRunner] instances.
type JobRunners struct {
	runners []*JobRunner
	results FinalizedJobs
	limiter JobLimiter
}

// NewJobRunners creates a slice of [JobRunner] instances from a slice of [Job].
func NewJobRunners[JobT Job](jobs []JobT, opts JobRunnerOpts) JobRunners {
	digits := ndigits(len(jobs) - 1)

	runners := make([]*JobRunner, len(jobs))
	for i, job := range jobs {
		id := fmt.Sprintf("%0*d", digits, i)
		runners[i] = NewJobRunner(job, id, opts)
	}

	return JobRunners{
		runners: runners,
		results: make(FinalizedJobs, len(jobs)),
	}
}

func ndigits(n int) (digits int) {
	for n > 0 {
		n /= 10
		digits++
	}
	return
}

func (j JobRunners) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(j.runners))
	for _, runner := range j.runners {
		cmds = append(cmds, runner.Init())
	}
	return tea.Batch(cmds...)
}

func (j JobRunners) Update(msg tea.Msg) (JobRunners, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case JobResultMessage:
		i := slices.Index(j.runners, msg.Job)
		if i == -1 {
			break
		}

		slog.Debug(
			"picked up on job result message",
			"job_id", msg.Job.id,
			"job_index", i)

		j.results[i] = &msg

		if j.results.IsDone() {
			cmds = append(cmds, func() tea.Msg { return j.results })
		}
	}

	for i, runner := range j.runners {
		j.runners[i], cmd = runner.Update(msg)
		cmds = append(cmds, cmd)
	}

	return j, tea.Batch(cmds...)
}

var footerStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("6")) // cyan

func (j JobRunners) View() string {
	var b strings.Builder
	for _, runner := range j.runners {
		b.WriteString(runner.View())
	}

	b.WriteString(footerStyle.Render("∑ "))
	b.WriteString(footerStyle.Render(fmt.Sprintf(
		"%d/%d", j.results.NumFinishedJobs(), len(j.results),
	)))

	if len(j.results) > 0 && j.results.IsDone() {
		longestJob := slices.MaxFunc(j.results, func(a, b *JobResultMessage) int {
			return cmp.Compare(a.TimeTaken, b.TimeTaken)
		})
		b.WriteString(" ")
		b.WriteString(footerStyle.Faint(true).Render(fmt.Sprintf(
			"[%s]", longestJob.TimeTaken.Round(TimeTakenAccuracy).String(),
		)))
	}

	b.WriteString("\n")

	return b.String()
}

// Finalize returns the job results if all jobs are done. If not, it returns nil
// and false. If all jobs are done, it returns the results and true.
func (j JobRunners) Finalize() (FinalizedJobs, bool) {
	if j.results.IsDone() {
		return j.results, true
	}
	return nil, false
}

// Stop returns a [tea.Cmd] that will stop all the jobs in the [JobRunners]
// collection.
func (j JobRunners) Stop() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(j.runners))
	for _, runner := range j.runners {
		cmds = append(cmds, runner.Stop())
	}
	return tea.Batch(cmds...)
}

// FinalizedJobs contains the results of jobs.
// It is always of length equal to the number of jobs that have been run, but
// jobs that have not yet completed will have a nil value in the slice.
type FinalizedJobs []*JobResultMessage

// HasError checks if any of the job result messages in the slice contain an
// error.
func (j FinalizedJobs) HasError() bool {
	return slices.ContainsFunc(j, func(msg *JobResultMessage) bool {
		return msg != nil && msg.HasError()
	})
}

// Errors collects all the errors from the job result messages in the slice.
func (j FinalizedJobs) Errors() []error {
	var errs []error
	for _, msg := range j {
		if msg != nil && msg.HasError() {
			errs = append(errs, msg.Error)
		}
	}
	return errs
}

// IsDone checks if all the job result messages in the slice are done, meaning
// there is no nil values.
func (j FinalizedJobs) IsDone() bool {
	return !slices.ContainsFunc(j, func(j *JobResultMessage) bool {
		return j == nil || !j.IsDone()
	})
}

// NumFinishedJobs counts the number of jobs that have completed.
func (j FinalizedJobs) NumFinishedJobs() int {
	var count int
	for _, msg := range j {
		if msg != nil && msg.IsDone() {
			count++
		}
	}
	return count
}
