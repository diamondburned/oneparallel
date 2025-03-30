package oneparallel

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/stopwatch"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"libdb.so/oneparallel/internal/xtea"
)

// TimeTakenAccuracy is the accuracy for measuring the time taken for a job.
// This is used for displaying the elapsed time for a job in the UI.
const TimeTakenAccuracy = time.Millisecond

// JobResultMessage is a message that contains the result of a job.
type JobResultMessage struct {
	Job       *JobRunner
	Error     error
	TimeTaken time.Duration
}

// IsStarted returns true if the job has started running.
// A finished job is still considered started, so this will return true.
func (j *JobResultMessage) IsStarted() bool { return j != nil && j.Job != nil }

// IsDone returns true if the job has finished running.
func (j *JobResultMessage) IsDone() bool { return j.IsStarted() && j.TimeTaken > 0 }

// HasError returns true if the job has an error.
func (j *JobResultMessage) HasError() bool { return j.IsStarted() && j.Error != nil }

// JobRunner is a running [Job] instance.
type JobRunner struct {
	job      Job
	id       string
	opts     JobRunnerOpts
	result   *JobResultMessage
	outFiles []string

	resultCh   chan JobResultMessage
	stopwatch  stopwatch.Model
	lineBuffer LineBuffer
}

type JobRunnerOpts struct {
	JobLimiter      JobLimiter
	LastLines       int
	OutputDir       string
	CombinedOutputs bool
}

// NewJobRunner creates a new [JobRunner].
func NewJobRunner(job Job, id string, opts JobRunnerOpts) *JobRunner {
	var outFiles []string
	if opts.OutputDir != "" {
		if opts.CombinedOutputs {
			outFiles = []string{
				filepath.Join(opts.OutputDir, fmt.Sprintf("%s.txt", id)),
			}
		} else {
			outFiles = []string{
				filepath.Join(opts.OutputDir, fmt.Sprintf("%s-stdout.txt", id)),
				filepath.Join(opts.OutputDir, fmt.Sprintf("%s-stderr.txt", id)),
			}
		}
	}

	return &JobRunner{
		job:      job,
		id:       id,
		opts:     opts,
		outFiles: outFiles,

		resultCh:   make(chan JobResultMessage, 1),
		stopwatch:  stopwatch.NewWithInterval(100 * time.Millisecond),
		lineBuffer: *newLineBuffer(jobBufferStyle, opts.LastLines),
	}
}

func (j *JobRunner) start() error {
	slog := slog.With("job", j.id)

	stdout, err := j.job.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	defer stdout.Close()

	stderr, err := j.job.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}
	defer stderr.Close()

	rmap := map[io.Reader][]LineWriter{
		stdout: make([]LineWriter, 0, 2),
		stderr: make([]LineWriter, 0, 2),
	}

	rmap[stdout] = append(rmap[stdout], j.lineBuffer.LineWriter())
	rmap[stderr] = append(rmap[stderr], j.lineBuffer.LineWriter())

	if j.opts.OutputDir != "" {
		if err := os.MkdirAll(j.opts.OutputDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}

		if j.opts.CombinedOutputs {
			path := j.outFiles[0]

			f, err := openFileLineWriter(path)
			if err != nil {
				return fmt.Errorf("failed to open file for combined output: %w", err)
			}
			defer f.Close()

			slog.Debug(
				"created combined output file",
				"path", path)

			rmap[stdout] = append(rmap[stdout], f)
			rmap[stderr] = append(rmap[stderr], f)
		} else {
			stdoutPath := j.outFiles[0]
			stderrPath := j.outFiles[1]

			stdoutFile, err := openFileLineWriter(stdoutPath)
			if err != nil {
				return fmt.Errorf("failed to open file for stdout: %w", err)
			}
			defer stdoutFile.Close()

			stderrFile, err := openFileLineWriter(stderrPath)
			if err != nil {
				return fmt.Errorf("failed to open file for stderr: %w", err)
			}
			defer stderrFile.Close()

			slog.Debug(
				"created output files",
				"stdout_path", stdoutPath,
				"stderr_path", stderrPath)

			rmap[stdout] = append(rmap[stdout], stdoutFile)
			rmap[stderr] = append(rmap[stderr], stderrFile)

		}
	}

	slog.Debug("starting job")

	if err := j.job.Start(); err != nil {
		return err
	}

	slog.Debug(
		"starting I/O routines",
		"stdout", stdout,
		"stderr", stderr,
		"rmap", rmap)

	if err := startReadingLinesForReaders(rmap); err != nil {
		return fmt.Errorf("failed to read job output: %w", err)
	}

	slog.Debug("waiting for job to finish")

	if err := j.job.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			slog.Debug(
				"job failed because process exited with non-zero status",
				"exit_code", exitErr.ExitCode())
		}

		return err
	}

	slog.Debug("job completed successfully")
	return nil
}

func (j *JobRunner) Init() tea.Cmd {
	go func() {
		release := j.opts.JobLimiter.Acquire()
		defer release()

		result := JobResultMessage{Job: j}
		j.resultCh <- result

		t := time.Now()
		result.Error = j.start()
		result.TimeTaken = time.Since(t)
		j.resultCh <- result
	}()

	return tea.Batch(
		j.lineBuffer.Init(),
		xtea.ChannelCmd(j.resultCh),
	)
}

func (j *JobRunner) Update(msg tea.Msg) (*JobRunner, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case JobResultMessage:
		if msg.Job != j {
			break
		}

		old := j.result
		switch {
		case !old.IsStarted() && msg.IsStarted():
			cmds = append(cmds, j.stopwatch.Start())
		case !old.IsDone() && msg.IsDone():
			cmds = append(cmds, j.stopwatch.Stop())
		}

		j.result = &msg

	case tea.InterruptMsg:
		if err := j.job.Stop(); err != nil {
			slog.Error(
				"failed to signal job stop",
				"job", j.id,
				"err", err)
		}
	}

	j.lineBuffer, cmd = j.lineBuffer.Update(msg)
	cmds = append(cmds, cmd)

	j.stopwatch, cmd = j.stopwatch.Update(msg)
	cmds = append(cmds, cmd)

	cmds = append(cmds, xtea.ChannelCmd(j.resultCh))

	return j, tea.Batch(cmds...)
}

const (
	prefixHeader = "╭╴"
	prefixBuffer = "│"
	prefixFooter = "╰╴"
)

var jobHeaderStyle = lipgloss.NewStyle().
	Bold(true).
	MaxWidth(50)

var jobQueuedStyle = jobHeaderStyle.
	Foreground(lipgloss.NoColor{}).
	Faint(true)

var jobErrorStyle = jobHeaderStyle.
	Foreground(lipgloss.Color("9")) // red

var jobDoneStyle = jobHeaderStyle.
	Foreground(lipgloss.Color("2")) // green

var jobOutputStyle = lipgloss.NewStyle().
	Bold(true).
	Faint(true)

var jobDurationStyle = lipgloss.NewStyle().
	Faint(true)

var jobBorderStyle = lipgloss.NewStyle()

var jobBufferStyle = lipgloss.NewStyle().
	PaddingLeft(1).
	BorderLeft(true).
	BorderStyle(lipgloss.Border{Left: prefixBuffer})

func (j *JobRunner) View() string {
	var b strings.Builder

	var style lipgloss.Style
	switch {
	case !j.result.IsStarted():
		style = jobQueuedStyle
	case !j.result.IsDone():
		style = jobHeaderStyle
	case j.result.Error != nil:
		style = jobErrorStyle
	default:
		style = jobDoneStyle
	}

	b.WriteString(jobBorderStyle.Render(prefixHeader) + style.Render(j.job.String()))

	if len(j.outFiles) > 0 {
		b.WriteString(" ")
		b.WriteString(jobOutputStyle.Render("[" + strings.Join(j.outFiles, " + ") + "]"))
	}

	timeTaken := j.stopwatch.Elapsed()
	if j.result.IsDone() {
		// Prefer the actual measured time taken from the job result if available.
		// Otherwise, use the elapsed time from the stopwatch.
		timeTaken = j.result.TimeTaken
	}

	b.WriteString(" ")
	b.WriteString(jobDurationStyle.Render(fmt.Sprintf(
		"[%s]", timeTaken.Round(TimeTakenAccuracy).String(),
	)))

	if j.result != nil && j.result.Error != nil {
		b.WriteString(" ")

		var exitErr *exec.ExitError
		if errors.As(j.result.Error, &exitErr) {
			b.WriteString(jobErrorStyle.Bold(false).Render(
				fmt.Sprintf("(exit status %d)", exitErr.ExitCode()),
			))
		} else {
			b.WriteString(jobErrorStyle.Bold(false).Render(
				fmt.Sprintf("(error: %s)", j.result.Error),
			))
		}
	}

	b.WriteString("\n")

	b.WriteString(j.lineBuffer.View())

	b.WriteString(jobBorderStyle.Render(prefixFooter))

	b.WriteString("\n")

	return b.String()
}
