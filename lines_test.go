package oneparallel

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"libdb.so/oneparallel/internal/xtea"
)

func TestLineBuffer(t *testing.T) {
	testLineBufferStyle := lipgloss.NewStyle()
	defaultTermsize := [2]int{80, 24}

	tests := []struct {
		name     string
		height   int
		termSize [2]int
		inLines  []string
		outLines []string
	}{
		{
			name:     "ordering",
			height:   3,
			termSize: defaultTermsize,
			inLines:  []string{"1", "2", "3", "4", "5", "6"},
			outLines: []string{"4", "5", "6"},
		},
		{
			name:     "standard_overflow",
			height:   3,
			termSize: defaultTermsize,
			inLines: []string{
				strings.Repeat("meow", 30),
			},
			outLines: []string{
				"",
				"meowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeow",
				"meowmeowmeowmeowmeowmeowmeowmeowmeowmeow",
			},
		},
		{
			name:     "multiline_overflow",
			height:   3,
			termSize: defaultTermsize,
			inLines: []string{
				"hi1",
				"hi2",
				strings.Repeat("meow", 30),
			},
			outLines: []string{
				"hi2",
				"meowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeowmeow",
				"meowmeowmeowmeowmeowmeowmeowmeowmeowmeow",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			l := newLineBuffer(testLineBufferStyle, test.height)

			tm := teatest.NewTestModel(t,
				xtea.WrappedModel[LineBuffer]{Model: l},
				teatest.WithInitialTermSize(test.termSize[0], test.termSize[1]))

			for _, line := range test.inLines {
				tm.Send(lineBufferMsg{src: l.msgCh, line: line})
			}
			tm.Quit()

			l = tm.FinalModel(t).(xtea.WrappedModel[LineBuffer]).Model

			got := trimLineSpaces(l.View())
			t.Logf("final output:\n%s", got)

			want := strings.Join(test.outLines, "\n") + "\n"
			if got != want {
				t.Errorf(
					"output mismatch:\nwant:\n%s\ngot:\n%s",
					debugString(want),
					debugString(got),
				)
			}
		})
	}
}

func trimLineSpaces(s string) string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = strings.TrimSpace(l)
	}
	return strings.Join(ls, "\n")
}

func debugString(s string) string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = strconv.Quote(l)
	}
	return strings.Join(ls, "\n")
}
