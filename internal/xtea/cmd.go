package xtea

import tea "github.com/charmbracelet/bubbletea"

// ChannelCmd returns a [tea.Cmd] that waits for a value to be sent on the given
// channel.
func ChannelCmd[T any](ch chan T) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
