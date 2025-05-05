package xtea

import tea "github.com/charmbracelet/bubbletea"

type WrappedModel[T interface {
	Init() tea.Cmd
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}] struct {
	Model T
}

func (j WrappedModel[T]) Init() tea.Cmd { return j.Model.Init() }

func (j WrappedModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := j.Model.Update(msg)
	return WrappedModel[T]{m}, cmd
}

func (j WrappedModel[T]) View() string {
	return j.Model.View()
}
