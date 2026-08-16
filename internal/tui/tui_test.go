package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// step feeds one message through Update and returns the resulting model.
func step(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func TestModel_SuccessfulRunView(t *testing.T) {
	m := newModel()
	m = step(m, startMsg{runName: "20260816T000000Z", dir: "out/run", tables: []string{"users", "orders"}})
	m = step(m, tableStartMsg{table: "users"})
	m = step(m, tableRowsMsg{table: "users", rows: 1234567})
	m = step(m, tableDoneMsg{table: "users", rows: 1234567, elapsed: 1200 * time.Millisecond})
	m = step(m, tableStartMsg{table: "orders"})
	m = step(m, tableDoneMsg{table: "orders", rows: 42, elapsed: 30 * time.Millisecond})
	m = step(m, doneMsg{runName: "20260816T000000Z", dir: "out/run", elapsed: 1500 * time.Millisecond})

	next, cmd := m.Update(resultMsg{runDir: "out/run"})
	m = next.(model)
	if cmd == nil {
		t.Fatal("resultMsg should return a quit command")
	}
	if m.runDir != "out/run" || m.err != nil {
		t.Fatalf("result not captured: runDir=%q err=%v", m.runDir, m.err)
	}

	view := m.View()
	for _, want := range []string{
		"digestive export",
		"20260816T000000Z",
		"users",
		"orders",
		"1,234,567 rows", // thousands separators
		"2/2 tables",
		"export complete",
		"out/run",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestModel_FailureView(t *testing.T) {
	m := newModel()
	m = step(m, startMsg{tables: []string{"users"}})
	m = step(m, tableStartMsg{table: "users"})
	m = step(m, failedMsg{table: "users", err: errors.New("scan row: boom")})

	next, cmd := m.Update(resultMsg{err: errors.New("export table \"users\": scan row: boom")})
	m = next.(model)
	if cmd == nil {
		t.Fatal("resultMsg should return a quit command")
	}

	view := m.View()
	for _, want := range []string{"scan row: boom", "export failed"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestModel_WarningsRendered(t *testing.T) {
	m := newModel()
	m = step(m, startMsg{tables: []string{"events"}})
	m = step(m, warnMsg{msg: "events.payload: json_anonymise redacted 3 unparseable cell(s)"})

	if got := m.View(); !strings.Contains(got, "json_anonymise redacted 3") {
		t.Errorf("warning not rendered:\n%s", got)
	}
}

func TestModel_WindowsLongList(t *testing.T) {
	names := make([]string, 30)
	for i := range names {
		names[i] = "t" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	m := newModel()
	m = step(m, startMsg{tables: names})
	// Complete the first 20, make table 20 active.
	for i := 0; i < 20; i++ {
		m = step(m, tableDoneMsg{table: names[i], rows: 100, elapsed: time.Second})
	}
	m = step(m, tableStartMsg{table: names[20]})

	view := m.View()
	// At most maxVisibleTables table rows, plus the two collapse markers.
	tableLines := 0
	for _, line := range strings.Split(view, "\n") {
		for _, n := range names {
			if strings.Contains(line, " "+n) {
				tableLines++
				break
			}
		}
	}
	if tableLines > maxVisibleTables {
		t.Errorf("showed %d table rows, want <= %d\n%s", tableLines, maxVisibleTables, view)
	}
	if !strings.Contains(view, "done") || !strings.Contains(view, "pending") {
		t.Errorf("expected collapse markers for hidden tables:\n%s", view)
	}
	// The active table must always be visible.
	if !strings.Contains(view, names[20]) {
		t.Errorf("active table %q not in window:\n%s", names[20], view)
	}
}

func TestHumanCount(t *testing.T) {
	cases := map[int64]string{0: "0", 42: "42", 1000: "1,000", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", in, got, want)
		}
	}
}
