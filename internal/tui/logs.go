package tui

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const logTail = 32 << 10 // bytes of the log file to show

type logMsg struct {
	text string
	err  error
}

// tailLog reads the end of the log file. It runs as a Cmd so file I/O never
// happens in Update or View.
func tailLog(path string) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return logMsg{text: "(no log yet)"}
		}
		if err != nil {
			return logMsg{err: err}
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return logMsg{err: err}
		}
		off := max(0, fi.Size()-logTail)
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return logMsg{err: err}
		}
		b, err := io.ReadAll(f)
		if err != nil {
			return logMsg{err: err}
		}
		text := string(b)
		if off > 0 { // drop the partial first line
			if i := strings.IndexByte(text, '\n'); i >= 0 {
				text = text[i+1:]
			}
		}
		return logMsg{text: strings.TrimRight(text, "\n")}
	}
}

type logsModel struct {
	vp   viewport.Model
	text string // raw, unwrapped
	err  error
}

func newLogs() *logsModel { return &logsModel{vp: viewport.New(40, 10)} }

func (l *logsModel) resize(w, h int) {
	l.vp.Width, l.vp.Height = w, h
	l.render(false)
}

// render wraps the text to the viewport width: a clipped error message is
// worse than a wrapped one.
func (l *logsModel) render(follow bool) {
	wrapped := lipgloss.NewStyle().Width(max(10, l.vp.Width)).Render(l.text)
	l.vp.SetContent(wrapped)
	if follow {
		l.vp.GotoBottom()
	}
}

func (l *logsModel) set(m logMsg) {
	if m.err != nil {
		l.err = m.err
		return
	}
	l.err = nil
	if m.text == l.text {
		return
	}
	follow := l.vp.AtBottom() || l.text == ""
	l.text = m.text
	l.render(follow)
}

func (l *logsModel) update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "G" {
		l.vp.GotoBottom()
		return nil
	}
	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(msg)
	return cmd
}

// view renders at height h (a copy of the viewport, so drawing never mutates
// the model).
func (l *logsModel) view(th theme, h int) string {
	if l.err != nil {
		return th.errText().Render("log: " + l.err.Error())
	}
	vp := l.vp
	vp.Height = h
	return vp.View()
}
