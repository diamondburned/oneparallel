package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/pflag"
	"libdb.so/oneparallel"
)

var (
	ErrJobsFailed = errors.New("one or more jobs failed")
)

const usage = "oneparallel [flags] <command> <command_arg> -- <job_args> ..."
const examples = `
examples:
  build Nix files in parallel:
  $ oneparallel -- nix-build ::: *.nix

  run 3 commands in parallel:
  $ oneparallel -- sh -c ::: ls df "echo hi"

  run a command 3 times:
  $ oneparallel -n0 -- sh -c "echo hi; sleep 2; echo bye" ::: 1 2 3
`

var (
	njobs          = 0
	nargs          = 1
	lastLines      = 1
	outputDir      = ""
	separateOutput = false
	dryRun         = false
	debug          = false
)

func init() {
	pflag.CommandLine.Init("oneparallel", pflag.ContinueOnError)

	pflag.IntVarP(&njobs, "jobs", "j", njobs, "number of jobs to run, default is unlimited")
	pflag.IntVarP(&nargs, "args", "n", nargs, "number of args to pass to each job")
	pflag.IntVarP(&lastLines, "lines", "l", lastLines, "number of lines to print from each job's output")
	pflag.StringVarP(&outputDir, "output", "o", outputDir, "directory to write job outputs to (each job is numbered)")
	pflag.BoolVarP(&separateOutput, "separate", "s", separateOutput, "write stdout and stderr to separate files (if true, [id]-stdout.txt and [id]-stderr.txt, else [id].txt)")
	pflag.BoolVar(&dryRun, "dry-run", dryRun, "print commands without executing them")

	pflag.BoolVar(&debug, "debug", debug, "print debug logs")
	pflag.CommandLine.MarkHidden("debug")

	pflag.Usage = func() {
		log.Println("usage:", usage)
		log.Println()
		log.Println("flags:")
		pflag.PrintDefaults()
		log.Println()
		log.Println(strings.TrimSpace(examples))
	}
}

func main() {
	log.SetFlags(0)

	ok, err := run()
	if err != nil && !errors.Is(err, pflag.ErrHelp) {
		log.SetOutput(os.Stderr)
		log.Println("error:", err)
	}
	if !ok {
		os.Exit(1)
	}
}

func run() (bool, error) {
	if err := pflag.CommandLine.Parse(os.Args[1:]); err != nil {
		return errors.Is(err, pflag.ErrHelp), err
	}

	if pflag.NArg() == 0 {
		log.Println("usage:", usage)
		log.Println("see --help for more information")
		return false, nil
	}

	args := pflag.CommandLine.Args()

	parsed, err := parseArgsToCommands(args)
	if err != nil {
		return false, err
	}

	jobs := oneparallel.NewExecJobs(parsed.commands...)

	if dryRun {
		for _, job := range jobs {
			fmt.Println(job.String())
		}
		return true, nil
	}

	for i, j := range jobs {
		// Set a custom per-job string for clarity.
		j.SetCustomString(oneparallel.QuotedArgs(parsed.jobArgs[i]))
	}

	runners := oneparallel.NewJobRunners(jobs, oneparallel.JobRunnerOpts{
		JobLimiter:      oneparallel.NewJobLimiter(njobs),
		LastLines:       lastLines,
		OutputDir:       outputDir,
		CombinedOutputs: !separateOutput,
	})

	if debug {
		logPath := filepath.Join(os.TempDir(), "oneparallel-debug.log")
		log.Println("debug mode enabled, logging to:", logPath)

		logFile, err := tea.LogToFile(logPath, "")
		if err != nil {
			return false, fmt.Errorf("failed to set up debug logging: %w", err)
		}
		defer logFile.Close()

		slog.SetDefault(
			slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})),
		)
	} else {
		log.SetOutput(io.Discard)
	}

	if _, err := tea.NewProgram(newModel(runners)).Run(); err != nil {
		return false, err
	}

	finalized, ok := runners.Finalize()
	if !ok {
		return false, errors.New("failed to finalize job runners")
	}

	return !finalized.HasError(), nil
}

type parsedArgs struct {
	commands []*exec.Cmd
	jobArgs  [][]string
}

func parseArgsToCommands(args []string) (parsedArgs, error) {
	separatedAt := slices.Index(args, ":::")
	if separatedAt == -1 {
		return parsedArgs{}, errors.New("missing ::: separator for arguments, see --help")
	}

	baseCommand := args[:separatedAt]
	jobArgsList := args[separatedAt+1:]

	parsed := parsedArgs{
		commands: make([]*exec.Cmd, 0, len(jobArgsList)/max(nargs, 1)+1),
		jobArgs:  nil,
	}

	if nargs == 0 {
		if len(baseCommand) == 0 {
			return parsedArgs{}, errors.New("no command provided")
		}
		// if nargs is 0, then just do one command N times.
		parsed.jobArgs = slices.Repeat([][]string{baseCommand}, len(jobArgsList))
	} else {
		// regular behavior
		parsed.jobArgs = slices.Collect(slices.Chunk(jobArgsList, max(nargs, 1)))
	}

	for _, args := range parsed.jobArgs {
		if nargs != 0 && len(args) != nargs {
			// This only works at the end of the chunks.
			return parsedArgs{}, fmt.Errorf("invalid multiple of job args: got %d, want %d", len(args), nargs)
		}

		if len(baseCommand) > 0 {
			args = slices.Concat(baseCommand, args)
		}

		cmd := exec.Command(args[0], args[1:]...)
		parsed.commands = append(parsed.commands, cmd)
	}

	return parsed, nil
}

type model struct {
	viewport viewport.Model
	runners  oneparallel.JobRunners
	altMode  bool
}

func newModel(runners oneparallel.JobRunners) *model {
	return &model{
		viewport: viewport.New(0, 0),
		runners:  runners,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.runners.Init(),
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	slog.Debug(
		"received message in model update",
		"msg_type", fmt.Sprintf("%T", msg))

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height

		cmds = append(cmds, m.updateJobHeights())

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			cmds = append(cmds, m.runners.Stop())
		case "f":
			cmds = append(cmds, m.SetFullscreen(!m.altMode))
		}

	case fullscreenMsg:
		m.altMode = msg.fullscreen
		cmds = append(cmds, m.updateJobHeights())
	}

	m.runners, cmd = m.runners.Update(msg)
	cmds = append(cmds, cmd)

	if m.altMode {
		m.viewport.SetContent(m.runners.View())
	}

	// If the runners are already done, then we quit the program after
	// everything is executed.
	if _, finalized := m.runners.Finalize(); finalized {
		// Exit alt mode first so that the latest view is seen in the
		// scrollback.
		if m.altMode {
			cmds = append(cmds, m.SetFullscreen(false))
			return m, tea.Batch(cmds...)
		}

		// If we're still switching fullscreen, then don't quit yet.
		// Let this run first.
		_, isFullscreenMsg := msg.(fullscreenMsg)
		if isFullscreenMsg {
			return m, tea.Batch(cmds...)
		}

		return m, tea.Sequence(tea.Batch(cmds...), tea.Quit)
	} else {
		return m, tea.Batch(cmds...)
	}
}

func (m *model) updateJobHeights() tea.Cmd {
	if m.altMode {
		return m.runners.SetTotalHeight(m.viewport.Height)
	}
	return m.runners.SetJobHeight(lastLines)
}

type fullscreenMsg struct {
	fullscreen bool
}

// SetFullscreen returns a command that toggles fullscreen mode.
func (m *model) SetFullscreen(fullscreen bool) tea.Cmd {
	msg := func() tea.Msg { return fullscreenMsg{fullscreen} }
	if fullscreen {
		return tea.Sequence(tea.EnterAltScreen, msg)
	} else {
		return tea.Sequence(tea.ExitAltScreen, msg)
	}
}

func (m *model) View() string {
	if m.altMode {
		return m.viewport.View()
	} else {
		return m.runners.View()
	}
}
