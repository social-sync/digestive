// Package tui renders a live, in-place progress display for an export run.
//
// It drives a Bubble Tea program from the structured events emitted by
// export.Run (via export.Reporter). The export itself runs in a Bubble Tea
// command goroutine; its events are forwarded to the program as messages, so
// all rendering stays on Bubble Tea's single event loop.
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/danmatthews/grimnir/internal/export"
)

// Run displays the progress UI while exec runs the export, and returns exec's
// result once the run finishes. exec is called with a Reporter it must pass to
// export.Run so events reach the UI.
func Run(exec func(export.Reporter) (string, error)) (string, error) {
	rep := &sender{}
	m := newModel()
	m.exec = func() tea.Msg {
		dir, err := exec(rep)
		return resultMsg{runDir: dir, err: err}
	}

	// Render to stderr so stdout stays clean for the machine-readable run dir.
	prog := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	rep.prog = prog

	final, err := prog.Run()
	if err != nil {
		return "", err
	}
	fm := final.(model)
	return fm.runDir, fm.err
}

// sender is an export.Reporter that forwards every event to the Bubble Tea
// program as a message.
type sender struct{ prog *tea.Program }

func (s *sender) Start(runName, dir string, tables []string) {
	s.prog.Send(startMsg{runName: runName, dir: dir, tables: tables})
}
func (s *sender) TableStart(table string) { s.prog.Send(tableStartMsg{table: table}) }
func (s *sender) TableRows(table string, rows int64) {
	s.prog.Send(tableRowsMsg{table: table, rows: rows})
}
func (s *sender) TableDone(table string, rows int64, elapsed time.Duration) {
	s.prog.Send(tableDoneMsg{table: table, rows: rows, elapsed: elapsed})
}
func (s *sender) Warn(msg string)                { s.prog.Send(warnMsg{msg: msg}) }
func (s *sender) Failed(table string, err error) { s.prog.Send(failedMsg{table: table, err: err}) }
func (s *sender) Done(runName, dir string, elapsed time.Duration) {
	s.prog.Send(doneMsg{runName: runName, dir: dir, elapsed: elapsed})
}

type (
	startMsg struct {
		runName, dir string
		tables       []string
	}
	tableStartMsg struct{ table string }
	tableRowsMsg  struct {
		table string
		rows  int64
	}
	tableDoneMsg struct {
		table   string
		rows    int64
		elapsed time.Duration
	}
	warnMsg   struct{ msg string }
	failedMsg struct {
		table string
		err   error
	}
	doneMsg struct {
		runName, dir string
		elapsed      time.Duration
	}
	// resultMsg carries exec's return value and ends the program.
	resultMsg struct {
		runDir string
		err    error
	}
)

type status int

const (
	pending status = iota
	active
	done
	failed
)

type tableState struct {
	name    string
	status  status
	rows    int64
	elapsed time.Duration
	started time.Time
	err     error
}

type model struct {
	exec    func() tea.Msg
	spinner spinner.Model

	runName string
	dir     string
	tables  []tableState
	byName  map[string]int
	warns   []string

	finished bool
	elapsed  time.Duration

	// Result of exec, read by Run after the program exits.
	runDir string
	err    error
}

func newModel() model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleSpinner
	return model{spinner: sp, byName: map[string]int{}}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.exec)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

	case spinner.TickMsg:
		if m.finished {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case startMsg:
		m.runName, m.dir = msg.runName, msg.dir
		m.tables = make([]tableState, len(msg.tables))
		for i, name := range msg.tables {
			m.tables[i] = tableState{name: name, status: pending}
			m.byName[name] = i
		}

	case tableStartMsg:
		if t := m.table(msg.table); t != nil {
			t.status = active
			t.started = time.Now()
		}

	case tableRowsMsg:
		if t := m.table(msg.table); t != nil {
			t.rows = msg.rows
		}

	case tableDoneMsg:
		if t := m.table(msg.table); t != nil {
			t.status = done
			t.rows = msg.rows
			t.elapsed = msg.elapsed
		}

	case warnMsg:
		m.warns = append(m.warns, msg.msg)

	case failedMsg:
		if t := m.table(msg.table); t != nil {
			t.status = failed
			t.err = msg.err
		}

	case doneMsg:
		m.finished = true
		m.elapsed = msg.elapsed

	case resultMsg:
		m.runDir, m.err = msg.runDir, msg.err
		m.finished = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) table(name string) *tableState {
	if i, ok := m.byName[name]; ok {
		return &m.tables[i]
	}
	return nil
}

func (m model) View() string {
	var b strings.Builder

	title := styleTitle.Render("grimnir export")
	if m.runName != "" {
		title += "  " + styleDim.Render(m.runName)
	}
	b.WriteString(title + "\n\n")

	var doneCount int
	var totalRows int64
	activeIdx := -1
	for i := range m.tables {
		switch m.tables[i].status {
		case done:
			doneCount++
		case active:
			activeIdx = i
		}
		totalRows += m.tables[i].rows
	}
	if activeIdx < 0 {
		// Nothing active (not started yet, or finished): anchor on the tail.
		activeIdx = len(m.tables) - 1
	}

	// Render only a fixed-height window so long table lists don't scroll the
	// terminal. The export is sequential, so everything before the window is
	// done and everything after is pending; both are collapsed into one marker.
	start, end := windowBounds(activeIdx, len(m.tables))
	if start > 0 {
		b.WriteString("  " + styleDim.Render(fmt.Sprintf("⋯ %d done", start)) + "\n")
	}
	for _, t := range m.tables[start:end] {
		b.WriteString("  " + m.renderTable(t) + "\n")
	}
	if end < len(m.tables) {
		b.WriteString("  " + styleDim.Render(fmt.Sprintf("⋯ %d pending", len(m.tables)-end)) + "\n")
	}

	if len(m.warns) > 0 {
		b.WriteString("\n")
		for _, w := range m.warns {
			b.WriteString("  " + styleWarn.Render("⚠ "+w) + "\n")
		}
	}

	b.WriteString("\n")
	summary := fmt.Sprintf("%d/%d tables · %s rows", doneCount, len(m.tables), humanCount(totalRows))
	if m.finished && m.err == nil {
		b.WriteString("  " + styleOK.Render("✓ export complete") +
			styleDim.Render("  "+summary+" · "+fmtDur(m.elapsed)) + "\n")
		if m.runDir != "" {
			b.WriteString("  " + styleDim.Render(m.runDir) + "\n")
		}
	} else if m.err != nil {
		b.WriteString("  " + styleErr.Render("✗ export failed") + "\n")
	} else {
		b.WriteString("  " + styleDim.Render(summary) + "\n")
	}

	return b.String()
}

func (m model) renderTable(t tableState) string {
	switch t.status {
	case active:
		return fmt.Sprintf("%s %s  %s  %s",
			m.spinner.View(),
			t.name,
			styleDim.Render(humanCount(t.rows)+" rows"),
			styleDim.Render(fmtDur(time.Since(t.started))))
	case done:
		return fmt.Sprintf("%s %s  %s  %s",
			styleOK.Render("✓"),
			t.name,
			styleDim.Render(humanCount(t.rows)+" rows"),
			styleDim.Render(fmtDur(t.elapsed)))
	case failed:
		line := styleErr.Render("✗") + " " + t.name
		if t.err != nil {
			line += "  " + styleErr.Render(t.err.Error())
		}
		return line
	default:
		return styleDim.Render("○ " + t.name)
	}
}

// Windowing bounds for the table list, keeping output height fixed.
const (
	// maxVisibleTables caps how many table rows are shown at once.
	maxVisibleTables = 10
	// windowLookback is how many completed tables to keep visible above the
	// active one for context before it scrolls off.
	windowLookback = 3
)

// windowBounds returns the [start, end) slice of a list of n tables to display,
// keeping at most maxVisibleTables rows and keeping index active visible with a
// little lookback context above it.
func windowBounds(active, n int) (int, int) {
	w := maxVisibleTables
	if w > n {
		w = n
	}
	start := active - windowLookback
	if start < 0 {
		start = 0
	}
	if start > n-w {
		start = n - w
	}
	if start < 0 {
		start = 0
	}
	return start, start + w
}

var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleSpinner = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
)

// humanCount formats n with thousands separators (e.g. 1234567 -> "1,234,567").
func humanCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}

// fmtDur renders a duration compactly (sub-second in ms, otherwise seconds).
func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
