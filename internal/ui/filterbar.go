package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/jtrull101/tcpview/internal/filter"
)

// filterBarState holds everything about the "/"-triggered filter editor:
// the text input, the last successfully applied Filter (and its source
// text, so re-opening the editor starts from what's currently active
// rather than blank), and any parse error from the most recent attempt.
type filterBarState struct {
	editing bool
	input   textinput.Model
	active  filter.Filter
	text    string // raw expression behind `active`, "" means no filter
	err     error
}

func newFilterBarState() filterBarState {
	ti := textinput.New()
	ti.Placeholder = "proto=tcp dport=443 src=10.0.0.0/24"
	ti.CharLimit = 200
	ti.Width = 60
	return filterBarState{input: ti}
}

func (f *filterBarState) startEditing() {
	f.editing = true
	f.err = nil
	f.input.SetValue(f.text)
	f.input.CursorEnd()
}

func (f *filterBarState) cancelEditing() {
	f.editing = false
	f.err = nil
	f.input.Blur()
}

// apply commits a successfully parsed filter and exits edit mode.
func (f *filterBarState) apply(active filter.Filter) {
	f.active = active
	f.text = f.input.Value()
	f.editing = false
	f.err = nil
	f.input.Blur()
}

func (f filterBarState) view() string {
	if f.editing {
		s := fmt.Sprintf("filter: %s\n", f.input.View())
		if f.err != nil {
			s += fmt.Sprintf("  %v\n", f.err)
		}
		s += "enter apply  esc cancel\n"
		return s
	}
	if f.text != "" {
		return fmt.Sprintf("filter: %s  (/ edit, clear with an empty expression)\nq quit\n", f.text)
	}
	return "/ filter  q quit\n"
}
