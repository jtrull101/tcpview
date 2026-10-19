package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/filter"
)

var errBogusExpr = errors.New("filter: invalid term")

// --- positive ---

func TestFilterBar_SlashEntersEditingMode(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)
	if !m.filterBar.editing {
		t.Fatal("filterBar.editing = false after '/'")
	}
}

func TestFilterBar_EnterAppliesValidExpressionAndNarrowsRows(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	// one TCP packet, one UDP packet - a "proto=udp" filter should keep
	// only the UDP one.
	tcp := samplePacket(1)
	udp := samplePacket(2)
	udp.Proto = "UDP"

	for _, p := range []tea.Msg{packetMsg(tcp), packetMsg(udp)} {
		mUpdated, _ := m.Update(p)
		m = mUpdated.(Model)
	}

	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)
	for _, r := range "proto=udp" {
		mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mUpdated.(Model)
	}
	mUpdated, _ = m.Update(keyMsg("enter"))
	m = mUpdated.(Model)

	if m.filterBar.editing {
		t.Fatal("still editing after enter with a valid expression")
	}
	if len(m.table.Rows()) != 1 {
		t.Fatalf("table has %d rows after filtering to proto=udp, want 1", len(m.table.Rows()))
	}
}

func TestFilterBar_ViewShowsErrorWhileEditing(t *testing.T) {
	t.Parallel()
	fb := newFilterBarState()
	fb.editing = true
	fb.err = errBogusExpr
	if out := fb.view(); !strings.Contains(out, errBogusExpr.Error()) {
		t.Fatalf("view() = %q, want it to mention the parse error", out)
	}
}

func TestFilterBar_ViewShowsActiveFilterText(t *testing.T) {
	t.Parallel()
	fb := newFilterBarState()
	fb.text = "proto=tcp"
	if out := fb.view(); !strings.Contains(out, "proto=tcp") {
		t.Fatalf("view() = %q, want it to mention the active filter text", out)
	}
}

// --- negative ---

func TestFilterBar_EnterWithInvalidExpressionStaysEditingWithError(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)
	for _, r := range "bogus" {
		mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mUpdated.(Model)
	}
	mUpdated, _ = m.Update(keyMsg("enter"))
	m = mUpdated.(Model)

	if !m.filterBar.editing {
		t.Fatal("exited editing mode despite an invalid expression")
	}
	if m.filterBar.err == nil {
		t.Fatal("filterBar.err is nil after an invalid expression")
	}
}

func TestFilterBar_EscCancelsWithoutApplying(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.filterBar.text = "proto=tcp"
	active, err := filter.ParseExpression("proto=tcp")
	if err != nil {
		t.Fatalf("ParseExpression setup error: %v", err)
	}
	m.filterBar.active = active

	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)
	for _, r := range "proto=udp" {
		mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mUpdated.(Model)
	}
	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mUpdated.(Model)

	if m.filterBar.editing {
		t.Fatal("still editing after esc")
	}
	if m.filterBar.text != "proto=tcp" {
		t.Fatalf("filterBar.text = %q after cancel, want unchanged proto=tcp", m.filterBar.text)
	}
}

func TestFilterBar_QDoesNotQuitWhileEditing(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)
	mUpdated, cmd := m.Update(keyMsg("q"))
	m = mUpdated.(Model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, isQuit := msg.(tea.QuitMsg); isQuit {
				t.Fatal("'q' triggered tea.Quit while editing a filter")
			}
		}
	}
	if m.filterBar.input.Value() != "q" {
		t.Fatalf("filterBar.input.Value() = %q, want 'q' to have been typed into the input", m.filterBar.input.Value())
	}
}

func TestFilterBar_CtrlCStillQuitsWhileEditing(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)
	_, cmd := m.Update(keyMsg("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c returned nil cmd while editing a filter, want tea.Quit")
	}
	if !fs.closed {
		t.Fatal("source was not closed on ctrl+c while editing")
	}
}

// --- load ---

func TestFilterBar_Load_ManyKeystrokesFast(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)

	for i := 0; i < 5000; i++ {
		mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		m = mUpdated.(Model)
	}
	if len(m.filterBar.input.Value()) != 200 { // CharLimit caps it
		t.Fatalf("input length = %d, want capped at CharLimit 200", len(m.filterBar.input.Value()))
	}
}

// --- stress ---

func TestFilterBar_Stress_RepeatedEditApplyCancelCycles(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	exprs := []string{"proto=tcp", "bogus", "dport=443", "", "src=10.0.0.0/8"}
	for i := 0; i < 200; i++ {
		mUpdated, _ := m.Update(keyMsg("/"))
		m = mUpdated.(Model)
		expr := exprs[i%len(exprs)]
		m.filterBar.input.SetValue(expr)
		if i%2 == 0 {
			mUpdated, _ = m.Update(keyMsg("enter"))
		} else {
			mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		}
		m = mUpdated.(Model)
	}
}

// --- chaos ---

func TestFilterBar_Chaos_RandomKeysWhileEditingNeverPanic(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	mUpdated, _ := m.Update(keyMsg("/"))
	m = mUpdated.(Model)

	runes := []rune("=/.: \t0123456789abcXYZ")
	for i := 0; i < 1000; i++ {
		r := runes[i%len(runes)]
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked on rune %q at i=%d: %v", r, i, rec)
				}
			}()
			mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			m = mUpdated.(Model)
		}()
		if i%7 == 0 {
			mUpdated, _ := m.Update(keyMsg("enter"))
			m = mUpdated.(Model)
			if !m.filterBar.editing {
				// a valid expression landed and applied - re-open for the rest of the loop
				mUpdated, _ = m.Update(keyMsg("/"))
				m = mUpdated.(Model)
			}
		}
	}
}
