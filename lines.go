package oneparallel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sync/errgroup"
	"libdb.so/oneparallel/internal/xtea"
)

// LineWriter is an interface for writing lines of text. It is used to
// write lines to different outputs, such as a buffer or a file.
type LineWriter interface {
	WriteLine([]byte) error
}

type lineBufferMsg struct {
	src  chan lineBufferMsg
	line string
}

// MaxLineBufferHeight is the maximum number of lines that can be buffered in
// the [LineBuffer].
const MaxLineBufferHeight = 128

// LineBuffer is a UI element that buffers the last few lines of text.
type LineBuffer struct {
	msgCh chan lineBufferMsg
	style lipgloss.Style
	lines []string

	Width  int
	Height int
}

func newLineBuffer(style lipgloss.Style, height int) LineBuffer {
	return LineBuffer{
		msgCh:  make(chan lineBufferMsg),
		style:  style,
		lines:  make([]string, MaxLineBufferHeight),
		Height: height,
	}
}

// LineWriter returns a LineWriter that writes lines to [l].
func (l LineBuffer) LineWriter() LineWriter {
	return lineBufferWriter{l.msgCh}
}

type lineBufferWriter struct {
	msgCh chan lineBufferMsg
}

func (l lineBufferWriter) WriteLine(line []byte) error {
	l.msgCh <- lineBufferMsg{
		src:  l.msgCh,
		line: string(line),
	}
	return nil
}

func (l LineBuffer) Init() tea.Cmd {
	return xtea.ChannelCmd(l.msgCh)
}

type lineBufferHeightMsg struct {
	height int
}

// SetHeight sets the height of the line buffer.
// The height can not be higher than
func (l LineBuffer) SetHeight(height int) tea.Cmd {
	height = min(height, MaxLineBufferHeight)
	height = max(height, 0)
	return func() tea.Msg {
		return lineBufferHeightMsg{height: height}
	}
}

func (l LineBuffer) Update(msg tea.Msg) (LineBuffer, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.Width = msg.Width

	case lineBufferHeightMsg:
		l.Height = msg.height

	case lineBufferMsg:
		if msg.src != l.msgCh {
			break
		}
		copy(l.lines, l.lines[1:]) // shift left
		l.lines[len(l.lines)-1] = msg.line
	}

	return l, xtea.ChannelCmd(l.msgCh)
}

// View returns the buffered lines as a single string.
func (l LineBuffer) View() string {
	style := l.style.Width(l.Width)
	allLines := make([]string, 0, l.Height)

	// Iterate last lines first so we can account for line wrapping.
	for _, line := range slices.Backward(l.lines) {
		lines := strings.Split(style.Render(line), "\n")

		allLines = slices.Insert(allLines, 0, lines...)

		if len(allLines) >= l.Height {
			allLines = allLines[len(allLines)-l.Height:]
			break
		}
	}

	return strings.Join(allLines, "\n") + "\n"
}

type fileLineWriter struct {
	f *os.File
	b *bufio.Writer
}

var (
	_ LineWriter = (*fileLineWriter)(nil)
	_ io.Closer  = (*fileLineWriter)(nil)
)

func openFileLineWriter(filePath string) (*fileLineWriter, error) {
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return nil, err
	}
	b := bufio.NewWriter(f)
	return &fileLineWriter{f, b}, nil
}

func (l *fileLineWriter) WriteLine(line []byte) error {
	_, werr := l.b.Write(line)
	return errors.Join(
		werr,
		l.b.WriteByte('\n'),
		l.b.Flush(),
	)
}

func (l *fileLineWriter) Close() error {
	return errors.Join(
		l.b.Flush(),
		l.f.Close(),
	)
}

// startReadingLines blocks and reads lines from the provided io.Reader, writing
// them to the provided LineWriter(s). The LineWriters must be concurrent-safe
// so that multiple startReadingLines can be spun up without issue.
func startReadingLines(r io.Reader, writers ...LineWriter) error {
	var lines int

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines++
		for _, writer := range writers {
			if err := writer.WriteLine(scanner.Bytes()); err != nil {
				return fmt.Errorf("line writer error: %w", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Warn(
			"failed to read from reader",
			"reader", r,
			"err", err)
	}

	slog.Debug(
		"reader goroutine finished reading",
		"reader", r,
		"writers", writers,
		"lines", lines)

	return nil
}

// startReadingLinesForReaders starts reading lines from multiple io.Readers and writes
// them to their corresponding LineWriters. It blocks until all readers are done
// then returns any error encountered during reading.
func startReadingLinesForReaders(rmap map[io.Reader][]LineWriter) error {
	var errg errgroup.Group
	for r, writers := range rmap {
		errg.Go(func() error {
			return startReadingLines(r, writers...)
		})
	}
	return errg.Wait()
}
