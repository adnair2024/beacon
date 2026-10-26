package tui

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the TUI on a real terminal and, in ModeEngine, the engine loop
// beside it. When the UI exits the engine is cancelled and waited for, so
// the engine lock is released and a final coalesced state is flushed.
func Run(o Options) error {
	o.Dark = detectDark(o.Cfg.TUI.Theme) // before the program owns the terminal
	m := newApp(o)
	p := tea.NewProgram(m, tea.WithAltScreen())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	if o.Engine != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := o.Engine(ctx, func(err error) { p.Send(errMsg{err}) })
			p.Send(engineDoneMsg{err})
		}()
	}
	_, err := p.Run()
	cancel()
	wg.Wait()
	return err
}
