// Package ui implements tcpview's terminal UI with Bubble Tea. It reads
// from a capture.Source and store.Ring but never decides capture engine
// details itself - local vs. remote is resolved by whatever constructs the
// Model, so the UI code is identical either way.
package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/export"
	"github.com/jtrull101/tcpview/internal/filter"
	"github.com/jtrull101/tcpview/internal/store"
)

type viewState int

const (
	viewPickInterface viewState = iota
	viewLiveTail
	viewDetail
	viewHelp
)

const ringCapacity = 1000

// SourceOpener lazily opens a capture.Source for the interface the user
// picked. Kept as a function (rather than opening eagerly for every listed
// interface) so capture only actually starts once the user makes a choice.
type SourceOpener func(ifaceName string) (capture.Source, error)

// Model is tcpview's root Bubble Tea model.
type Model struct {
	state  viewState
	ifaces []capture.Interface
	cursor int

	label  string // interface name, or a remote host label
	host   string // remote host being captured on, "" for local capture
	open   SourceOpener
	source capture.Source
	ring   *store.Ring
	table  table.Model
	dirty  bool // true when the ring or the active filter changed since the last table rebuild

	// openGen counts interface opens requested via the picker or tab
	// switching. Each openSource result carries the gen it was requested
	// under, so a slow open that's been superseded by a later tab press is
	// closed and dropped instead of replacing the newer source.
	openGen int
	// ticking is true once the tickCmd chain is running; it re-arms itself
	// for the rest of the program, so switching sources must not start a
	// second one.
	ticking bool

	// displayed mirrors table's rows 1:1 - the table only stores string
	// cells, so this is how a table cursor position maps back to a full
	// Packet (with Raw bytes) for the detail view or export. viewDetail and
	// viewHelp are only ever entered from viewLiveTail, so leaving either
	// always returns there - no separate "previous state" needs tracking.
	displayed []store.Packet
	selected  store.Packet

	filterBar filterBarState
	timeline  timelineState

	status    string // transient informational message (e.g. "exported N packets to ..."), distinct from err
	exportDir string // directory export writes land in; "" means the process's cwd

	// archive/archiveReader are nil unless EnableArchive was called -
	// archiving to disk is opt-in (main.go wires it from a flag), not a
	// hard dependency of the UI.
	archive       *store.Archive
	archiveReader *store.ArchiveReader

	err           error
	width, height int
}

func newTable() table.Model {
	columns := []table.Column{
		{Title: "Time", Width: 12},
		{Title: "Proto", Width: 6},
		{Title: "Source", Width: 24},
		{Title: "Destination", Width: 24},
		{Title: "Len", Width: 6},
	}
	return table.New(table.WithColumns(columns), table.WithFocused(true), table.WithHeight(20))
}

// NewModel starts in the interface-picker view; capture begins once the
// user selects one, via open.
func NewModel(ifaces []capture.Interface, open SourceOpener) Model {
	return Model{
		state:     viewPickInterface,
		ifaces:    ifaces,
		open:      open,
		ring:      store.NewRing(ringCapacity, 0),
		table:     newTable(),
		filterBar: newFilterBarState(),
	}
}

// NewModelWithSource skips the picker and starts live-tailing an
// already-open source immediately - used for remote capture, where there's
// no local interface list to choose from (and so no tab switching).
func NewModelWithSource(label string, source capture.Source) Model {
	return Model{
		state:     viewLiveTail,
		label:     label,
		source:    source,
		ticking:   source != nil, // Init starts the tick chain
		ring:      store.NewRing(ringCapacity, 0),
		table:     newTable(),
		filterBar: newFilterBarState(),
	}
}

// SetRemoteHost marks the model as capturing on host over SSH: interface
// labels become "host (iface)" and the picker says which host it's
// listing. Call before Init/the first Update.
func (m *Model) SetRemoteHost(host string) {
	m.host = host
}

// SetInterfaces enables tab/shift+tab switching for a model built with
// NewModelWithSource (remote capture started on an interface given up
// front), positioning the cursor on current. If current isn't in ifaces
// (e.g. the remote's tcpdump -D omitted it) it's added at the front, so
// cycling can always get back to it.
func (m *Model) SetInterfaces(ifaces []capture.Interface, open SourceOpener, current string) {
	idx := -1
	for i, iface := range ifaces {
		if iface.Name == current {
			idx = i
			break
		}
	}
	if idx < 0 {
		ifaces = append([]capture.Interface{{Name: current}}, ifaces...)
		idx = 0
	}
	m.ifaces = ifaces
	m.open = open
	m.cursor = idx
}

// ifaceLabel is what the header shows for capturing on iface.
func (m Model) ifaceLabel(iface string) string {
	if m.host == "" {
		return iface
	}
	return fmt.Sprintf("%s (%s)", m.host, iface)
}

// EnableArchive wires a bounded on-disk archive into the model: packets
// evicted from the in-memory ring get written to it instead of discarded,
// and rewind queries fall back to reading it for anything older than what
// the ring still holds. Optional - a Model with no archive just can't
// rewind past the ring's retention window.
func (m *Model) EnableArchive(a *store.Archive, r *store.ArchiveReader) {
	m.archive = a
	m.archiveReader = r
}

// SetStatus sets the transient informational line shown under the live
// view, e.g. for main to explain why something at startup was degraded.
func (m *Model) SetStatus(status string) {
	m.status = status
}

// SetExportDir sets the directory pcap exports (the "e" key) are written
// into. Defaults to the process's current working directory if never
// called.
func (m *Model) SetExportDir(dir string) {
	m.exportDir = dir
}

// exportCurrentView writes whatever's currently displayed (post-filter,
// live or rewound) to a new pcap file, named uniquely by the current time.
func (m *Model) exportCurrentView() {
	name := fmt.Sprintf("tcpview-export-%d.pcap", time.Now().UnixNano())
	path := filepath.Join(m.exportDir, name)
	n, err := export.WritePcap(path, m.displayed)
	if err != nil {
		m.err = err
		return
	}
	m.err = nil
	m.status = fmt.Sprintf("exported %d packet(s) to %s", n, path)
}

func (m Model) Init() tea.Cmd {
	if m.state == viewLiveTail && m.source != nil {
		return tea.Batch(waitForPacket(m.source), waitForSourceErr(m.source), tickCmd())
	}
	return nil
}

// --- messages bridging capture.Source's channels into Bubble Tea's
// message-driven Update loop ---

type packetMsg store.Packet
type packetsClosedMsg struct{}
type sourceErrMsg struct{ err error }
type sourceOpenedMsg struct {
	source capture.Source
	gen    int
}
type openErrMsg struct {
	err error
	gen int
}

// fromSourceMsg tags a message read off one specific source's channels.
// After a tab switch the old source's pending waitForPacket/
// waitForSourceErr reads still complete (with a last packet, or the
// close), and without the tag they'd be indistinguishable from the new
// source's - leaking old-interface packets into the new view and arming a
// duplicate reader on the new source.
type fromSourceMsg struct {
	source capture.Source
	msg    tea.Msg
}

// tickMsg drives the table redraw on a fixed cadence (see renderInterval)
// instead of on every packetMsg - rebuilding and restyling a 1000-row table
// on every single incoming packet doesn't scale to realistic capture rates
// (measured: ~50k rebuilds/sec is actually the ~10ms-per-rebuild regime,
// i.e. rebuilding per-packet caps the whole UI at ~100 packets/sec). No
// human needs more than renderInterval's worth of redraw rate anyway.
type tickMsg time.Time

const renderInterval = 100 * time.Millisecond

func tickCmd() tea.Cmd {
	return tea.Tick(renderInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func waitForPacket(source capture.Source) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-source.Packets()
		if !ok {
			return fromSourceMsg{source: source, msg: packetsClosedMsg{}}
		}
		return fromSourceMsg{source: source, msg: packetMsg(p)}
	}
}

func waitForSourceErr(source capture.Source) tea.Cmd {
	return func() tea.Msg {
		err, ok := <-source.Errors()
		if !ok {
			return nil
		}
		return fromSourceMsg{source: source, msg: sourceErrMsg{err: err}}
	}
}

func openSource(open SourceOpener, iface string, gen int) tea.Cmd {
	return func() tea.Msg {
		src, err := open(iface)
		if err != nil {
			return openErrMsg{err: err, gen: gen}
		}
		return sourceOpenedMsg{source: src, gen: gen}
	}
}

// closeSource closes src off the Update goroutine: closing a libpcap
// handle can block until its in-flight read times out, which would
// otherwise freeze the UI on every tab press.
func closeSource(src capture.Source) tea.Cmd {
	return func() tea.Msg {
		_ = src.Close()
		return nil
	}
}

// canSwitchInterface reports whether tab/shift+tab can move capture to
// another interface: that needs an interface list and an opener (remote
// capture started without SetInterfaces has neither), and somewhere else
// to go.
func (m Model) canSwitchInterface() bool {
	return m.open != nil && len(m.ifaces) > 1
}

// switchInterface moves capture delta positions through the interface
// list (wrapping at either end), local or remote alike: it closes the
// current source, hands the in-memory ring off to the archive (if
// enabled, so rewind can still reach the previous interface's traffic)
// and starts the view fresh for the new interface. The active filter is
// kept, same as it survives everything else.
func (m *Model) switchInterface(delta int) tea.Cmd {
	if !m.canSwitchInterface() {
		return nil
	}
	n := len(m.ifaces)
	m.cursor = ((m.cursor+delta)%n + n) % n
	name := m.ifaces[m.cursor].Name
	m.label = m.ifaceLabel(name)
	m.err = nil
	m.status = fmt.Sprintf("opening %s...", m.label)

	var cmds []tea.Cmd
	if m.source != nil {
		cmds = append(cmds, closeSource(m.source))
		m.source = nil
	}
	m.resetRing()
	m.timeline.jumpToLive()
	m.refreshRows()
	m.dirty = false

	m.openGen++
	cmds = append(cmds, openSource(m.open, name, m.openGen))
	return tea.Batch(cmds...)
}

// resetRing replaces the in-memory ring with an empty one, first writing
// everything it held to the archive (when enabled) exactly as if it had
// been evicted normally.
func (m *Model) resetRing() {
	old := m.ring.Snapshot()
	m.ring = store.NewRing(ringCapacity, 0)
	if m.archive == nil {
		return
	}
	for _, p := range old {
		if err := m.archive.Write(p); err != nil {
			m.err = err
			return
		}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.table.SetWidth(msg.Width)
		if h := msg.Height - 8; h > 0 {
			m.table.SetHeight(h)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case fromSourceMsg:
		if msg.source != m.source {
			return m, nil // from a source a tab switch already replaced
		}
		return m.Update(msg.msg)

	case sourceOpenedMsg:
		if msg.gen != m.openGen {
			return m, closeSource(msg.source) // superseded by a later switch
		}
		m.source = msg.source
		m.state = viewLiveTail
		m.status = ""
		cmds := []tea.Cmd{waitForPacket(m.source), waitForSourceErr(m.source)}
		if !m.ticking {
			m.ticking = true
			cmds = append(cmds, tickCmd())
		}
		return m, tea.Batch(cmds...)

	case openErrMsg:
		if msg.gen != m.openGen {
			return m, nil
		}
		m.err = msg.err
		m.status = ""
		return m, nil

	case packetMsg:
		evicted := m.ring.Add(store.Packet(msg))
		if m.archive != nil {
			for _, e := range evicted {
				if err := m.archive.Write(e); err != nil {
					m.err = err
				}
			}
		}
		m.dirty = true
		if m.source == nil {
			return m, nil
		}
		return m, waitForPacket(m.source)

	case packetsClosedMsg:
		return m, nil

	case sourceErrMsg:
		m.err = msg.err
		if m.source == nil {
			return m, nil
		}
		return m, waitForSourceErr(m.source)

	case tickMsg:
		if m.dirty {
			m.refreshRows()
			m.dirty = false
		}
		// Keep ticking through detail/help too: those overlays return to
		// the live view, which needs the chain still running to redraw.
		return m, tickCmd()
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits, even mid-filter-edit; plain "q" only quits
	// outside filter-editing, since "q" needs to be typeable there.
	if msg.String() == "ctrl+c" {
		if m.source != nil {
			_ = m.source.Close()
		}
		return m, tea.Quit
	}

	if m.state == viewDetail || m.state == viewHelp {
		// Both are read-only overlays entered from viewLiveTail - any key
		// closes them back to it, matching "press any key to continue".
		m.state = viewLiveTail
		return m, nil
	}

	if m.state == viewPickInterface {
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.ifaces)-1 {
				m.cursor++
			}
			return m, nil
		case "enter":
			if len(m.ifaces) == 0 {
				return m, nil
			}
			name := m.ifaces[m.cursor].Name
			m.label = m.ifaceLabel(name)
			m.openGen++
			return m, openSource(m.open, name, m.openGen)
		}
		return m, nil
	}

	if m.filterBar.editing {
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "q":
		if m.source != nil {
			_ = m.source.Close()
		}
		return m, tea.Quit
	case "/":
		m.filterBar.startEditing()
		return m, m.filterBar.input.Focus()
	case "left":
		m.timeline.stepBack(time.Now())
		m.refreshRows()
		return m, nil
	case "right":
		m.timeline.stepForward(time.Now())
		m.refreshRows()
		return m, nil
	case "L":
		m.timeline.jumpToLive()
		m.refreshRows()
		return m, nil
	case "enter":
		if cursor := m.table.Cursor(); cursor >= 0 && cursor < len(m.displayed) {
			m.selected = m.displayed[cursor]
			m.state = viewDetail
		}
		return m, nil
	case "tab":
		return m, m.switchInterface(1)
	case "shift+tab":
		return m, m.switchInterface(-1)
	case "?":
		m.state = viewHelp
		return m, nil
	case "e":
		m.exportCurrentView()
		return m, nil
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		f, err := filter.ParseExpression(m.filterBar.input.Value())
		if err != nil {
			m.filterBar.err = err
			return m, nil
		}
		m.filterBar.apply(f)
		m.refreshRows()
		m.dirty = false
		return m, nil
	case "esc":
		m.filterBar.cancelEditing()
		return m, nil
	}

	var cmd tea.Cmd
	m.filterBar.input, cmd = m.filterBar.input.Update(msg)
	return m, cmd
}

func (m *Model) refreshRows() {
	pkts := m.packetsForDisplay()
	rows := make([]table.Row, 0, len(pkts))
	displayed := make([]store.Packet, 0, len(pkts))
	for _, p := range pkts {
		if !m.filterBar.active.Matches(p) {
			continue
		}
		rows = append(rows, table.Row{
			p.Time.Format("15:04:05.000"),
			string(p.Proto),
			endpoint(p.SrcIP.String(), p.SrcPort),
			endpoint(p.DstIP.String(), p.DstPort),
			fmt.Sprintf("%d", p.Length),
		})
		displayed = append(displayed, p)
	}
	m.table.SetRows(rows)
	m.displayed = displayed
	if !m.timeline.active {
		// Live tail always pins to the newest row; a rewound view is a
		// fixed historical window that doesn't grow, so leave the user's
		// scroll position alone instead of yanking it to the bottom.
		m.table.GotoBottom()
	}
}

// packetsForDisplay returns the packets refreshRows should render: the
// full live ring when not rewound, or a merged, time-sorted view of
// whatever the ring and (if enabled) the on-disk archive hold within the
// current rewind window otherwise.
func (m Model) packetsForDisplay() []store.Packet {
	if !m.timeline.active {
		return m.ring.Snapshot()
	}

	from, to := m.timeline.window()
	pkts := m.ring.Range(from, to)
	if m.archiveReader != nil {
		if archived, err := m.archiveReader.Range(from, to); err == nil {
			pkts = append(pkts, archived...)
			sort.Slice(pkts, func(i, j int) bool { return pkts[i].Time.Before(pkts[j].Time) })
		}
	}
	return pkts
}

func endpoint(ip string, port uint16) string {
	if ip == "invalid IP" || ip == "" {
		ip = "?"
	}
	if port == 0 {
		return ip
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

func (m Model) View() string {
	switch m.state {
	case viewPickInterface:
		return m.viewPicker()
	case viewDetail:
		return m.viewDetail()
	case viewHelp:
		return helpText
	default:
		return m.viewLiveTail()
	}
}

func (m Model) viewDetail() string {
	var b strings.Builder
	b.WriteString("tcpview - packet detail\n\n")
	b.WriteString(renderDetail(m.selected))
	b.WriteString("\nesc/enter/q back\n")
	return b.String()
}

const helpText = `tcpview - keybindings

  up/down, j/k    move the table cursor / interface picker cursor
  enter           view packet detail (live-tail) / select interface (picker)
  tab / shift+tab switch capture to the next / previous interface
  /               edit the filter
  esc             cancel filter editing
  left / right    rewind backward / forward in time
  L               jump back to live
  e               export the currently shown packets to a pcap file
  ?               toggle this help
  q               quit (or go back, from detail/help)
  ctrl+c          quit immediately, from anywhere

press any key to close this help
`

func (m Model) viewPicker() string {
	var b strings.Builder
	if m.host != "" {
		fmt.Fprintf(&b, "tcpview - choose an interface on %s\n\n", m.host)
	} else {
		b.WriteString("tcpview - choose an interface\n\n")
	}
	if len(m.ifaces) == 0 {
		b.WriteString("  (no interfaces found)\n")
	}
	for i, iface := range m.ifaces {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		desc := iface.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(&b, "%s%-14s %s\n", cursor, iface.Name, desc)
	}
	b.WriteString("\n↑/↓ move  enter select  q quit\n")
	if m.err != nil {
		fmt.Fprintf(&b, "\nerror: %v\n", m.err)
	}
	return b.String()
}

func (m Model) viewLiveTail() string {
	var b strings.Builder
	modeLabel := "live"
	if m.timeline.active {
		modeLabel = "rewound"
	}
	current := m.label
	if m.canSwitchInterface() {
		current = fmt.Sprintf("%s [%d/%d, tab/shift+tab to switch]", m.label, m.cursor+1, len(m.ifaces))
	}
	fmt.Fprintf(&b, "tcpview - %s: %s  (%d packets shown)\n\n", modeLabel, current, len(m.table.Rows()))
	b.WriteString(m.table.View())
	b.WriteString("\n\n")
	b.WriteString(m.timeline.view())
	b.WriteString(m.filterBar.view())
	if m.status != "" {
		fmt.Fprintf(&b, "%s\n", m.status)
	}
	if m.err != nil {
		fmt.Fprintf(&b, "error: %v\n", m.err)
	}
	return b.String()
}
