package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
)

const (
	sysmonTickInterval = 2 * time.Second
	sparklineLength    = 20
	sysmonMinWidth     = 28
	brailleGraphRows   = 2
	coreHistoryLength  = 40 // data points per core for dot history
	projectTreeMaxRows = 512
	fileActivityTTL    = 15 * time.Second
)

// sparkBlocks maps a 0–7 intensity level to a Unicode block character.
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// braille dot bit positions: each character is a 2×4 grid.
// Left column (dots 1,2,3,7): rows 0-3 top to bottom.
// Right column (dots 4,5,6,8): rows 0-3 top to bottom.
var brailleLeftBits = [4]rune{0x01, 0x02, 0x04, 0x40}
var brailleRightBits = [4]rune{0x08, 0x10, 0x20, 0x80}

const brailleBase = '\u2800'

// sysmonTickMsg carries a periodic snapshot of system metrics.
type sysmonTickMsg struct {
	cpuPercent  float64
	perCore     []float64
	memPercent  float64
	memUsed     uint64
	memTotal    uint64
	memAvail    uint64
	memCached   uint64
	swapPercent float64
	swapUsed    uint64
	swapTotal   uint64
	netRecv     uint64
	netSent     uint64
	goroutines  int
	hostUptime  uint64
	loadAvg     [3]float64
	cpuTempAvg  float64
	cpuTempMax  float64
}

type projectTreeEntry struct {
	relPath string
	name    string
	depth   int
	isDir   bool
}

type fileActivity struct {
	role     bus.AgentRole
	lastSeen time.Time
}

// SysmonModel displays a compact system monitor panel.
type SysmonModel struct {
	cpuHistory    []float64
	coreHistories [][]float64
	netRecvHist   []float64
	netSentHist   []float64

	cpuPercent        float64
	perCore           []float64
	memPercent        float64
	memUsed           uint64
	memTotal          uint64
	memAvail          uint64
	memCached         uint64
	swapPercent       float64
	swapUsed          uint64
	swapTotal         uint64
	goroutines        int
	hostUptime        uint64
	loadAvg           [3]float64
	cpuTempAvg        float64
	cpuTempMax        float64
	recvRate          float64
	sentRate          float64
	prevNetRecv       uint64
	prevNetSent       uint64
	startTime         time.Time
	projectRoot       string
	projectTree       []projectTreeEntry
	treeScrollOffset  int
	treeFocused       bool // true when the user is navigating the tree (Tab)
	treeCursor        int  // selected entry index while focused
	activeFiles       map[string]fileActivity
	agentUsage        map[bus.AgentRole]bus.AgentUsage
	agentScrollOffset int

	width  int
	height int
}

// NewSysmon creates a new system monitor model.
func NewSysmon() SysmonModel {
	width := sysmonMinWidth
	return SysmonModel{
		cpuHistory:  make([]float64, graphHistoryPoints(width)),
		netRecvHist: make([]float64, graphHistoryPoints(width)),
		netSentHist: make([]float64, graphHistoryPoints(width)),
		startTime:   time.Now(),
		activeFiles: make(map[string]fileActivity),
		agentUsage:  make(map[bus.AgentRole]bus.AgentUsage),
		width:       width,
		height:      20,
	}
}

// Init starts the first tick.
func (m SysmonModel) Init() tea.Cmd {
	return sysmonTick()
}

// sysmonTick returns a command that collects system metrics after a delay.
func sysmonTick() tea.Cmd {
	return tea.Tick(sysmonTickInterval, func(time.Time) tea.Msg {
		// CPU — total + per-core.
		cpuTotal, _ := cpu.Percent(0, false)
		cpuPct := 0.0
		if len(cpuTotal) > 0 {
			cpuPct = cpuTotal[0]
		}
		perCore, _ := cpu.Percent(0, true)

		// Memory.
		vmStat, _ := mem.VirtualMemory()
		swapStat, _ := mem.SwapMemory()

		var memPct float64
		var memUsed, memTotal, memAvail, memCached uint64
		if vmStat != nil {
			memPct = vmStat.UsedPercent
			memUsed = vmStat.Used
			memTotal = vmStat.Total
			memAvail = vmStat.Available
			memCached = vmStat.Cached
		}
		var swapPct float64
		var swapUsed, swapTotal uint64
		if swapStat != nil {
			swapPct = swapStat.UsedPercent
			swapUsed = swapStat.Used
			swapTotal = swapStat.Total
		}

		// Network.
		counters, _ := net.IOCounters(false)
		var recv, sent uint64
		if len(counters) > 0 {
			recv = counters[0].BytesRecv
			sent = counters[0].BytesSent
		}

		// Host uptime.
		uptime, _ := host.Uptime()

		// Load average (unix).
		var loadAvg [3]float64
		if avg, err := load.Avg(); err == nil && avg != nil {
			loadAvg = [3]float64{avg.Load1, avg.Load5, avg.Load15}
		}

		cpuTempAvg, cpuTempMax := readCPUTemperature()

		return sysmonTickMsg{
			cpuPercent:  cpuPct,
			perCore:     perCore,
			memPercent:  memPct,
			memUsed:     memUsed,
			memTotal:    memTotal,
			memAvail:    memAvail,
			memCached:   memCached,
			swapPercent: swapPct,
			swapUsed:    swapUsed,
			swapTotal:   swapTotal,
			netRecv:     recv,
			netSent:     sent,
			goroutines:  runtime.NumGoroutine(),
			hostUptime:  uptime,
			loadAvg:     loadAvg,
			cpuTempAvg:  cpuTempAvg,
			cpuTempMax:  cpuTempMax,
		}
	})
}

// Update handles sysmon tick messages.
func (m SysmonModel) Update(msg tea.Msg) (SysmonModel, tea.Cmd) {
	tick, ok := msg.(sysmonTickMsg)
	if !ok {
		return m, nil
	}
	m.ensureGraphHistoryCapacity()

	m.cpuPercent = tick.cpuPercent
	m.perCore = tick.perCore
	m.memPercent = tick.memPercent
	m.memUsed = tick.memUsed
	m.memTotal = tick.memTotal
	m.memAvail = tick.memAvail
	m.memCached = tick.memCached
	m.swapPercent = tick.swapPercent
	m.swapUsed = tick.swapUsed
	m.swapTotal = tick.swapTotal
	m.goroutines = tick.goroutines
	m.hostUptime = tick.hostUptime
	m.loadAvg = tick.loadAvg
	m.cpuTempAvg = tick.cpuTempAvg
	m.cpuTempMax = tick.cpuTempMax

	// Calculate network rates (bytes/s).
	if m.prevNetRecv > 0 {
		m.recvRate = float64(tick.netRecv-m.prevNetRecv) / sysmonTickInterval.Seconds()
		m.sentRate = float64(tick.netSent-m.prevNetSent) / sysmonTickInterval.Seconds()
	}
	m.prevNetRecv = tick.netRecv
	m.prevNetSent = tick.netSent

	// Shift history and append new values.
	m.cpuHistory = append(m.cpuHistory[1:], m.cpuPercent)
	m.netRecvHist = append(m.netRecvHist[1:], m.recvRate)
	m.netSentHist = append(m.netSentHist[1:], m.sentRate)

	// Update per-core histories.
	coreCount := len(tick.perCore)
	if len(m.coreHistories) < coreCount {
		expanded := make([][]float64, coreCount)
		copy(expanded, m.coreHistories)
		for i := len(m.coreHistories); i < coreCount; i++ {
			expanded[i] = make([]float64, coreHistoryLength)
		}
		m.coreHistories = expanded
	}
	for i := 0; i < coreCount && i < len(m.coreHistories); i++ {
		m.coreHistories[i] = append(m.coreHistories[i][1:], tick.perCore[i])
	}
	m.pruneFileActivity(time.Now())
	m.refreshProjectTree()

	// Auto-advance tree scroll on each tick (paused while the user navigates).
	if len(m.projectTree) > 0 && !m.treeFocused {
		m.treeScrollOffset = (m.treeScrollOffset + 1) % len(m.projectTree)
	}

	// Auto-advance agent token-list scroll on each tick.
	activeAgents := 0
	for _, u := range m.agentUsage {
		if u.InputTokens+u.OutputTokens > 0 {
			activeAgents++
		}
	}
	if activeAgents > 0 {
		m.agentScrollOffset = (m.agentScrollOffset + 1) % activeAgents
	}

	return m, sysmonTick()
}

// SetSize updates the panel dimensions.
func (m *SysmonModel) SetSize(w, h int) {
	if w < sysmonMinWidth {
		w = sysmonMinWidth
	}
	m.width = w
	m.height = h
	m.ensureGraphHistoryCapacity()
}

// totalTokens returns the sum of input + output tokens across all agents.
func (m SysmonModel) totalTokens() int {
	total := 0
	for _, u := range m.agentUsage {
		total += u.InputTokens + u.OutputTokens
	}
	return total
}

// AgentUsage exposes the per-agent token accounting collected from the bus.
// The returned map is the live internal map — callers must treat it as
// read-only. This is the single source of truth shared between the system
// monitor panel and the end-of-run summary table, so the two views can
// never disagree.
func (m SysmonModel) AgentUsage() map[bus.AgentRole]bus.AgentUsage {
	return m.agentUsage
}

func (m *SysmonModel) ensureGraphHistoryCapacity() {
	target := graphHistoryPoints(m.width)
	m.cpuHistory = resizeHistory(m.cpuHistory, target)
	m.netRecvHist = resizeHistory(m.netRecvHist, target)
	m.netSentHist = resizeHistory(m.netSentHist, target)
}

func graphHistoryPoints(width int) int {
	contentW := width - 6
	if contentW < 8 {
		contentW = 8
	}
	points := contentW * 2
	if points < sparklineLength {
		return sparklineLength
	}
	return points
}

func resizeHistory(history []float64, target int) []float64 {
	if target <= 0 {
		target = sparklineLength
	}
	if len(history) == target {
		return history
	}
	resized := make([]float64, target)
	if len(history) == 0 {
		return resized
	}
	if len(history) >= target {
		copy(resized, history[len(history)-target:])
		return resized
	}
	copy(resized[target-len(history):], history)
	return resized
}

// SetProjectRoot configures the project root for the tree view.
func (m *SysmonModel) SetProjectRoot(root string) {
	m.projectRoot = root
	m.refreshProjectTree()
}

// FocusTree enters tree-navigation mode, pausing auto-scroll and placing the
// cursor on the first selectable file (falling back to the first entry).
func (m *SysmonModel) FocusTree() {
	m.treeFocused = true
	m.treeCursor = m.firstFileIndex()
}

// BlurTree leaves tree-navigation mode.
func (m *SysmonModel) BlurTree() { m.treeFocused = false }

// TreeFocused reports whether the tree is being navigated.
func (m SysmonModel) TreeFocused() bool { return m.treeFocused }

// MoveTreeCursor moves the selection by delta, clamped to the tree bounds.
func (m *SysmonModel) MoveTreeCursor(delta int) {
	if len(m.projectTree) == 0 {
		return
	}
	m.treeCursor += delta
	if m.treeCursor < 0 {
		m.treeCursor = 0
	}
	if m.treeCursor >= len(m.projectTree) {
		m.treeCursor = len(m.projectTree) - 1
	}
}

// SelectedTreeEntry returns the currently highlighted entry's repo-relative
// path and whether it is a directory. ok is false when the tree is empty.
func (m SysmonModel) SelectedTreeEntry() (relPath string, isDir bool, ok bool) {
	if len(m.projectTree) == 0 || m.treeCursor < 0 || m.treeCursor >= len(m.projectTree) {
		return "", false, false
	}
	e := m.projectTree[m.treeCursor]
	return e.relPath, e.isDir, true
}

// firstFileIndex returns the index of the first non-directory entry, or 0.
func (m SysmonModel) firstFileIndex() int {
	for i, e := range m.projectTree {
		if !e.isDir {
			return i
		}
	}
	return 0
}

// ObserveBusMessage updates active-file hints for the project tree.
func (m *SysmonModel) ObserveBusMessage(msg bus.Message) {
	now := time.Now()
	switch msg.Type {
	case bus.MsgRequest:
		role := msg.To
		for _, path := range m.pathsForRole(role, msg.Payload) {
			m.touchFile(path, role, now)
		}
	case bus.MsgEvent:
		if p, ok := msg.Payload.(bus.TokenPayload); ok {
			for _, path := range extractFilePathsFromText(p.Text) {
				m.touchFile(path, msg.From, now)
			}
		}
	case bus.MsgResponse:
		m.clearRoleActivity(msg.From)
	case bus.MsgUsage:
		if u, ok := msg.Payload.(bus.AgentUsage); ok {
			existing := m.agentUsage[msg.From]
			existing.InputTokens += u.InputTokens
			existing.OutputTokens += u.OutputTokens
			if u.Estimated {
				existing.Estimated = true
			}
			m.agentUsage[msg.From] = existing
		}
	}
	m.pruneFileActivity(now)
}

// View renders the system monitor panel.
func (m SysmonModel) View() string {
	contentW := m.width - 6
	if contentW < 20 {
		contentW = 20
	}

	borderColor := lipgloss.Color("#3b4261")
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ac3de")).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(crt.dim)
	valueStyle := lipgloss.NewStyle().Foreground(crt.bright).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	bSt := lipgloss.NewStyle().Foreground(borderColor)

	hRule := strings.Repeat("─", contentW+2)
	thinSep := bSt.Render("│") + dimStyle.Render(strings.Repeat("┄", contentW+2)) + bSt.Render("│")

	var lines []string
	lines = append(lines, bSt.Render("╭"+hRule+"╮"))

	addLine := func(content string) {
		vis := lipgloss.Width(content)
		if vis > contentW {
			// Defensive: never let a row overflow the right border. Truncate the
			// visible width while preserving ANSI escape sequences so colors are
			// preserved up to the cut point.
			content = ansi.Truncate(content, contentW, "…")
			vis = lipgloss.Width(content)
		}
		pad := contentW - vis
		if pad < 0 {
			pad = 0
		}
		lines = append(lines, bSt.Render("│")+" "+content+strings.Repeat(" ", pad)+" "+bSt.Render("│"))
	}

	addEmpty := func() {
		addLine("")
	}

	addSection := func(title string) {
		lines = append(lines, bSt.Render("├"+strings.Repeat("─", contentW+2)+"┤"))
		addLine(headerStyle.Render("  " + title))
	}

	addGraphLines := func(graphStr string) {
		for _, gl := range strings.Split(graphStr, "\n") {
			addLine(" " + gl)
		}
	}

	_ = addEmpty

	// ── Title ──
	addLine(titleStyle.Render("⚡ SYSTEM MONITOR"))
	addLine(dimStyle.Render(fmt.Sprintf("  %s • %d cores",
		formatHostUptime(m.hostUptime), runtime.NumCPU())))

	// ── CPU ──
	addSection("CPU")
	cpuColor := percentColor(m.cpuPercent)
	cpuStyle := lipgloss.NewStyle().Foreground(cpuColor).Bold(true)
	loadStr := dimStyle.Render(fmt.Sprintf("  load: %.1f %.1f %.1f",
		m.loadAvg[0], m.loadAvg[1], m.loadAvg[2]))
	addLine(cpuStyle.Render(fmt.Sprintf(" %5.1f%%", m.cpuPercent)) + loadStr)
	addGraphLines(renderBrailleGraph(m.cpuHistory, contentW, brailleGraphRows, cpuColor, 100.0))
	addLine(" " + renderSegmentedBar(m.cpuPercent, contentW))

	if m.cpuTempAvg > 0 {
		tempColor := tempPercentColor(m.cpuTempMax)
		tempStyle := lipgloss.NewStyle().Foreground(tempColor).Bold(true)
		addLine(labelStyle.Render(" Temp: ") +
			tempStyle.Render(fmt.Sprintf("%.0f°C", m.cpuTempAvg)) +
			dimStyle.Render(fmt.Sprintf("  max %.0f°C", m.cpuTempMax)))
	}

	// Per-core compact view with dot history: two cores per line.
	if len(m.perCore) > 0 {
		lines = append(lines, thinSep)
		coreCount := len(m.perCore)
		colW := contentW / 2
		for i := 0; i < coreCount; i += 2 {
			var leftHist []float64
			if i < len(m.coreHistories) {
				leftHist = m.coreHistories[i]
			}
			left := renderCoreDotted(i, m.perCore[i], leftHist, colW)
			right := ""
			if i+1 < coreCount {
				var rightHist []float64
				if i+1 < len(m.coreHistories) {
					rightHist = m.coreHistories[i+1]
				}
				right = renderCoreDotted(i+1, m.perCore[i+1], rightHist, colW)
			}
			addLine(left + right)
		}
		addLine(dimStyle.Render(fmt.Sprintf(" Load avg: %.2f %.2f %.2f",
			m.loadAvg[0], m.loadAvg[1], m.loadAvg[2])))
	}

	// ── MEM ──
	addSection("MEM")
	memColor := percentColor(m.memPercent)
	memStyle := lipgloss.NewStyle().Foreground(memColor).Bold(true)
	addLine(memStyle.Render(fmt.Sprintf(" %5.1f%%", m.memPercent)) +
		dimStyle.Render(fmt.Sprintf("  %s / %s", formatBytes(m.memUsed), formatBytes(m.memTotal))))
	addLine(" " + renderGradientBar(m.memPercent, contentW))

	availStr := labelStyle.Render(" Avail: ") + valueStyle.Render(formatBytes(m.memAvail))
	cachedStr := labelStyle.Render("  Cache: ") + valueStyle.Render(formatBytes(m.memCached))
	addLine(availStr + cachedStr)

	if m.swapTotal > 0 {
		swapColor := percentColor(m.swapPercent)
		swapStyle := lipgloss.NewStyle().Foreground(swapColor)
		swapLine := labelStyle.Render(" Swap:  ") +
			swapStyle.Render(fmt.Sprintf("%4.0f%%", m.swapPercent)) +
			dimStyle.Render(fmt.Sprintf("  %s / %s", formatBytes(m.swapUsed), formatBytes(m.swapTotal)))
		addLine(swapLine)
		addLine(" " + renderGradientBar(m.swapPercent, contentW))
	}

	// ── NET ──
	addSection("NET")
	netDnColor := lipgloss.Color("#73daca")
	netUpColor := lipgloss.Color("#7aa2f7")
	dnStyle := lipgloss.NewStyle().Foreground(netDnColor).Bold(true)
	upStyle := lipgloss.NewStyle().Foreground(netUpColor).Bold(true)
	addLine(dnStyle.Render(" ▼ "+formatRate(m.recvRate)) + "  " +
		upStyle.Render("▲ "+formatRate(m.sentRate)))
	addGraphLines(renderBrailleGraph(m.netRecvHist, contentW, brailleGraphRows, netDnColor, 0))
	addGraphLines(renderBrailleGraph(m.netSentHist, contentW, brailleGraphRows, netUpColor, 0))

	// ── TOKENS ── (agent list fixed at tokenAgentRows rows, auto-scrolling)
	const tokenAgentRows = 5
	if total := m.totalTokens(); total > 0 {
		addSection("TOKENS")
		totalColor := lipgloss.Color("#e0af68")
		totalStyle := lipgloss.NewStyle().Foreground(totalColor).Bold(true)
		addLine(labelStyle.Render(" Total: ") + totalStyle.Render(formatTokens(total)))

		renderAgentLine := func(role bus.AgentRole) string {
			u := m.agentUsage[role]
			roleStyle := lipgloss.NewStyle().Foreground(roleColor(string(role))).Bold(true)
			est := ""
			if u.Estimated {
				est = "~"
			}
			label := fmt.Sprintf(" %-11s", string(role))
			return roleStyle.Render(label) +
				dimStyle.Render(fmt.Sprintf("↓%s ↑%s%s",
					formatTokensCompact(u.InputTokens),
					formatTokensCompact(u.OutputTokens),
					est))
		}

		var visibleAgents []bus.AgentRole
		for _, role := range agentOrder {
			if u, ok := m.agentUsage[role]; ok && u.InputTokens+u.OutputTokens > 0 {
				visibleAgents = append(visibleAgents, role)
			}
		}

		totalAgents := len(visibleAgents)
		if totalAgents <= tokenAgentRows {
			for _, role := range visibleAgents {
				addLine(renderAgentLine(role))
			}
			for i := totalAgents; i < tokenAgentRows; i++ {
				addLine("")
			}
		} else {
			offset := m.agentScrollOffset % totalAgents
			rows := make([]string, tokenAgentRows)
			for i := 0; i < tokenAgentRows; i++ {
				idx := (offset + i) % totalAgents
				rows[i] = renderAgentLine(visibleAgents[idx])
			}
			rows[tokenAgentRows-1] = dimStyle.Render(fmt.Sprintf("  ↕ %d/%d", offset+1, totalAgents))
			for _, r := range rows {
				addLine(r)
			}
		}
	}

	// ── TREE ── (fixed height: always 10 content rows)
	const treeFixedRows = 10
	if m.treeFocused {
		addSection("TREE  [browsing]")
	} else {
		addSection("TREE")
	}
	treeLines := m.renderProjectTree(contentW, treeFixedRows)
	rendered := 0
	if len(treeLines) == 0 {
		addLine(dimStyle.Render(" (no project)"))
		rendered = 1
	} else {
		for _, line := range treeLines {
			addLine(line)
		}
		rendered = len(treeLines)
	}
	// Pad to the fixed height so the panel layout doesn't shift with content size.
	for i := rendered; i < treeFixedRows; i++ {
		addLine("")
	}

	// ── Info footer ──
	lines = append(lines, bSt.Render("├"+strings.Repeat("─", contentW+2)+"┤"))
	sessionLine := labelStyle.Render(" Session:    ") + valueStyle.Render(formatElapsed(time.Since(m.startTime).Round(time.Second)))
	addLine(sessionLine)

	// Bottom border.
	lines = append(lines, bSt.Render("╰"+hRule+"╯"))

	result := strings.Join(lines, "\n")

	// Vertically center if panel is taller than content; clip if shorter.
	totalLines := len(lines)
	if m.height > 0 && totalLines > m.height {
		clipped := strings.Split(result, "\n")
		result = strings.Join(clipped[:m.height], "\n")
	} else if m.height > totalLines {
		topPad := (m.height - totalLines) / 2
		result = strings.Repeat("\n", topPad) + result
	}

	return result
}

func (m *SysmonModel) refreshProjectTree() {
	if m.projectRoot == "" {
		m.projectTree = nil
		return
	}

	rootName := filepath.Base(m.projectRoot)
	entries := []projectTreeEntry{{relPath: ".", name: rootName + "/", depth: 0, isDir: true}}
	m.walkProjectTree(".", 0, &entries)
	m.projectTree = entries
}

func (m *SysmonModel) walkProjectTree(rel string, depth int, entries *[]projectTreeEntry) {
	if len(*entries) >= projectTreeMaxRows {
		return
	}
	abs := m.projectRoot
	if rel != "." {
		abs = filepath.Join(m.projectRoot, rel)
	}

	children, err := os.ReadDir(abs)
	if err != nil {
		return
	}

	sort.Slice(children, func(i, j int) bool {
		if children[i].IsDir() != children[j].IsDir() {
			return children[i].IsDir()
		}
		return children[i].Name() < children[j].Name()
	})

	for _, child := range children {
		name := child.Name()
		if shouldSkipTreeEntry(name, child.IsDir()) {
			continue
		}
		childRel := name
		if rel != "." {
			childRel = filepath.Join(rel, name)
		}
		entry := projectTreeEntry{
			relPath: filepath.ToSlash(childRel),
			name:    name,
			depth:   depth + 1,
			isDir:   child.IsDir(),
		}
		if entry.isDir {
			entry.name += "/"
		}
		*entries = append(*entries, entry)
		if child.IsDir() {
			m.walkProjectTree(childRel, depth+1, entries)
		}
		if len(*entries) >= projectTreeMaxRows {
			return
		}
	}
}

func shouldSkipTreeEntry(name string, isDir bool) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".idea", ".orchestrator":
		return true
	}
	if strings.HasPrefix(name, ".") && isDir {
		return true
	}
	return false
}

func (m SysmonModel) renderProjectTree(contentW, maxRows int) []string {
	if len(m.projectTree) == 0 || maxRows <= 0 {
		return nil
	}

	total := len(m.projectTree)
	dimSt := lipgloss.NewStyle().Foreground(crt.dim)

	// Focused navigation: window the list around the cursor and highlight it.
	if m.treeFocused {
		cursor := m.treeCursor
		if cursor >= total {
			cursor = total - 1
		}
		start := cursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start > total-maxRows {
			start = total - maxRows
		}
		if start < 0 {
			start = 0
		}
		var lines []string
		for i := 0; i < maxRows && start+i < total; i++ {
			idx := start + i
			lines = append(lines, m.renderTreeEntry(m.projectTree[idx], contentW, idx == cursor))
		}
		return lines
	}

	if total <= maxRows {
		// All entries fit — no scrolling needed.
		var lines []string
		for _, entry := range m.projectTree {
			lines = append(lines, m.renderTreeEntry(entry, contentW, false))
		}
		return lines
	}

	// Scroll: show maxRows entries starting from offset, wrapping around.
	offset := m.treeScrollOffset % total
	var lines []string
	for i := 0; i < maxRows; i++ {
		idx := (offset + i) % total
		lines = append(lines, m.renderTreeEntry(m.projectTree[idx], contentW, false))
	}
	// Scroll indicator in the last slot.
	indicator := dimSt.Render(fmt.Sprintf("  ↕ %d/%d", offset+1, total))
	lines[len(lines)-1] = indicator
	return lines
}

func (m SysmonModel) renderTreeEntry(entry projectTreeEntry, contentW int, selected bool) string {
	indent := strings.Repeat("  ", entry.depth)
	prefix := indent
	if entry.depth > 0 {
		prefix += "└─ "
	}
	label := prefix + entry.name

	maxW := contentW
	if maxW < 12 {
		maxW = 12
	}
	if lipgloss.Width(label) > maxW {
		runes := []rune(label)
		for i := len(runes); i > 0; i-- {
			candidate := string(runes[:i]) + "…"
			if lipgloss.Width(candidate) <= maxW {
				label = candidate
				break
			}
		}
	}

	if selected {
		return lipgloss.NewStyle().Reverse(true).Bold(true).Render(label)
	}

	if entry.isDir {
		style := lipgloss.NewStyle().Foreground(crt.dim)
		if m.hasActiveDescendant(entry.relPath) {
			style = lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
		}
		return style.Render(label)
	}

	if activity, ok := m.activeFiles[entry.relPath]; ok {
		return lipgloss.NewStyle().Foreground(roleColor(string(activity.role))).Bold(true).Render(label)
	}
	return lipgloss.NewStyle().Foreground(crt.bright).Render(label)
}

func (m SysmonModel) hasActiveDescendant(relPath string) bool {
	if relPath == "." {
		return len(m.activeFiles) > 0
	}
	prefix := relPath + "/"
	for path := range m.activeFiles {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (m *SysmonModel) touchFile(path string, role bus.AgentRole, now time.Time) {
	rel := sanitizeObservedPath(m.projectRoot, path)
	if rel == "" {
		return
	}
	m.activeFiles[rel] = fileActivity{role: role, lastSeen: now}
}

func (m *SysmonModel) clearRoleActivity(role bus.AgentRole) {
	for path, activity := range m.activeFiles {
		if activity.role == role {
			delete(m.activeFiles, path)
		}
	}
}

func (m *SysmonModel) pruneFileActivity(now time.Time) {
	for path, activity := range m.activeFiles {
		if now.Sub(activity.lastSeen) > fileActivityTTL {
			delete(m.activeFiles, path)
		}
	}
}

func (m SysmonModel) pathsForRole(role bus.AgentRole, payload any) []string {
	switch p := payload.(type) {
	case agent.CoderPayload:
		return append([]string{}, p.PriorFiles...)
	case agent.CoderFixPayload:
		return append([]string{}, p.Files...)
	case agent.QATestPayload:
		return append([]string{}, p.Files...)
	case agent.QAReviewPayload:
		return append([]string{}, p.Files...)
	case agent.SecurityPayload:
		return append([]string{}, p.Files...)
	case agent.UXReviewerPayload:
		return append([]string{}, p.Files...)
	}

	switch role {
	case bus.RolePM:
		return []string{".orchestrator/vision.md", ".orchestrator/moscow.md"}
	}
	return nil
}

func extractFilePathsFromText(text string) []string {
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '\n', '\t', '\r', '`', '"', '\'', '(', ')', '[', ']', '{', '}', ',', ':', ';':
			return true
		}
		return false
	})

	seen := map[string]bool{}
	var paths []string
	for _, field := range fields {
		if !strings.Contains(field, ".") || strings.HasPrefix(field, "http") {
			continue
		}
		field = strings.TrimSuffix(field, "…")
		field = strings.TrimPrefix(field, "./")
		if strings.Contains(field, "..") {
			continue
		}
		if !strings.Contains(field, "/") && !strings.HasPrefix(field, ".orchestrator/") {
			continue
		}
		if !seen[field] {
			seen[field] = true
			paths = append(paths, field)
		}
	}
	return paths
}

func sanitizeObservedPath(root, path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" || strings.Contains(path, "..") {
		return ""
	}

	if root != "" {
		rootSlash := filepath.ToSlash(root)
		if strings.HasPrefix(path, rootSlash+"/") {
			path = strings.TrimPrefix(path, rootSlash+"/")
		}
	}

	path = strings.TrimPrefix(path, "./")
	if path == "" {
		return ""
	}
	return path
}

// renderCoreDotted renders a single CPU core as a braille-dot history bar.
// Format: "C0 ⣀⣤⣶⣿⣶⣤⣀ 45%"
func renderCoreDotted(idx int, pct float64, history []float64, colW int) string {
	color := percentColor(pct)
	label := fmt.Sprintf("C%d", idx)
	pctStr := fmt.Sprintf("%3.0f%%", pct)

	barW := colW - len(label) - len(pctStr) - 3
	if barW < 3 {
		barW = 3
	}

	coreStyle := lipgloss.NewStyle().Foreground(crt.dim)
	pctStyle := lipgloss.NewStyle().Foreground(color)

	dotBar := renderBrailleDotBar(history, barW, color)

	return " " + coreStyle.Render(label) + " " + dotBar + " " + pctStyle.Render(pctStr)
}

// renderBrailleDotBar renders a 1-row braille dot history bar.
// Each character encodes 2 data points (left/right columns) × 4 dot rows.
func renderBrailleDotBar(history []float64, width int, color lipgloss.Color) string {
	dataPoints := width * 2
	data := make([]float64, dataPoints)
	if len(history) >= dataPoints {
		copy(data, history[len(history)-dataPoints:])
	} else if len(history) > 0 {
		offset := dataPoints - len(history)
		copy(data[offset:], history)
	}

	chars := make([]rune, width)
	for i := range chars {
		chars[i] = brailleBase
	}

	for i, val := range data {
		// Map percentage (0-100) to dot rows (0-4).
		level := int(math.Round(val / 100.0 * 4))
		if level > 4 {
			level = 4
		}

		charCol := i / 2
		if charCol >= width {
			continue
		}
		isRight := i%2 == 1

		for dotRow := 0; dotRow < level; dotRow++ {
			localRow := 3 - dotRow
			if isRight {
				chars[charCol] |= brailleRightBits[localRow]
			} else {
				chars[charCol] |= brailleLeftBits[localRow]
			}
		}
	}

	var sb strings.Builder
	for _, ch := range chars {
		sb.WriteRune(ch)
	}
	return lipgloss.NewStyle().Foreground(color).Render(sb.String())
}

// renderSegmentedBar renders a horizontal bar with colored segments and thin gaps.
func renderSegmentedBar(percent float64, maxWidth int) string {
	barWidth := maxWidth
	if barWidth < 4 {
		barWidth = 4
	}

	filled := int(math.Round(percent / 100.0 * float64(barWidth)))
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	segmentSize := 4 // gap every N characters
	emptyStyle := lipgloss.NewStyle().Foreground(crt.muted)

	var sb strings.Builder
	for i := 0; i < filled; i++ {
		bucketIdx := i * len(gradientColors) / barWidth
		if bucketIdx >= len(gradientColors) {
			bucketIdx = len(gradientColors) - 1
		}
		// Insert thin gap at segment boundaries.
		if i > 0 && i%segmentSize == 0 {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Render("▏"))
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(gradientColors[bucketIdx]).Render("█"))
		}
	}
	for i := 0; i < empty; i++ {
		pos := filled + i
		if pos > 0 && pos%segmentSize == 0 {
			sb.WriteString(emptyStyle.Render("▏"))
		} else {
			sb.WriteString(emptyStyle.Render("░"))
		}
	}
	return sb.String()
}

// renderBrailleGraph renders a braille-dot area chart (btop-style).
// Each character cell is 2 columns × 4 rows of dots, so `graphRows` character
// rows give 4*graphRows vertical resolution. When fixedMax is > 0 it is used
// as the ceiling (e.g. 100 for CPU%); otherwise the max is auto-detected.
func renderBrailleGraph(history []float64, width, graphRows int, color lipgloss.Color, fixedMax float64) string {
	totalDotRows := graphRows * 4
	dataPoints := width * 2

	data := make([]float64, dataPoints)
	if len(history) >= dataPoints {
		copy(data, history[len(history)-dataPoints:])
	} else {
		offset := dataPoints - len(history)
		copy(data[offset:], history)
	}

	maxVal := fixedMax
	if maxVal <= 0 {
		for _, v := range data {
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	grid := make([][]rune, graphRows)
	for r := range grid {
		grid[r] = make([]rune, width)
		for c := range grid[r] {
			grid[r][c] = brailleBase
		}
	}

	for i, val := range data {
		level := int(math.Round(val / maxVal * float64(totalDotRows)))
		if level > totalDotRows {
			level = totalDotRows
		}

		charCol := i / 2
		if charCol >= width {
			continue
		}
		isRight := i%2 == 1

		for dotRow := 0; dotRow < level; dotRow++ {
			charRow := graphRows - 1 - dotRow/4
			localRow := 3 - dotRow%4
			if charRow < 0 {
				continue
			}
			if isRight {
				grid[charRow][charCol] |= brailleRightBits[localRow]
			} else {
				grid[charRow][charCol] |= brailleLeftBits[localRow]
			}
		}
	}

	style := lipgloss.NewStyle().Foreground(color)
	var rowStrings []string
	for _, row := range grid {
		var sb strings.Builder
		for _, ch := range row {
			sb.WriteRune(ch)
		}
		rowStrings = append(rowStrings, style.Render(sb.String()))
	}
	return strings.Join(rowStrings, "\n")
}

// gradientColors defines the bar gradient from green (low) through yellow to red (high).
var gradientColors = []lipgloss.Color{
	"#9ece6a", "#9ece6a", // 0-19%  green
	"#b5c96a", "#c8c46a", // 20-39% green-yellow
	"#e0af68", "#e0af68", // 40-59% yellow
	"#ff9e64", "#ff9e64", // 60-79% orange
	"#f7768e", "#f7768e", // 80-100% red
}

// renderGradientBar renders a horizontal bar with a btop-style color gradient.
func renderGradientBar(percent float64, maxWidth int) string {
	barWidth := maxWidth
	if barWidth < 4 {
		barWidth = 4
	}

	filled := int(math.Round(percent / 100.0 * float64(barWidth)))
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	emptyStyle := lipgloss.NewStyle().Foreground(crt.muted)

	var sb strings.Builder
	for i := 0; i < filled; i++ {
		bucketIdx := i * len(gradientColors) / barWidth
		if bucketIdx >= len(gradientColors) {
			bucketIdx = len(gradientColors) - 1
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(gradientColors[bucketIdx]).Render("█"))
	}
	sb.WriteString(emptyStyle.Render(strings.Repeat("░", empty)))
	return sb.String()
}

// renderSparkline renders a Unicode sparkline from a history of values.
func renderSparkline(history []float64, maxWidth int) string {
	return renderSparklineColored(history, maxWidth, lipgloss.Color("#7aa2f7"))
}

// renderSparklineColored renders a sparkline with a custom color.
func renderSparklineColored(history []float64, maxWidth int, color lipgloss.Color) string {
	maxVal := 0.0
	for _, v := range history {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	visLen := len(history)
	if visLen > maxWidth {
		visLen = maxWidth
	}

	sparkStyle := lipgloss.NewStyle().Foreground(color)
	var sb strings.Builder
	start := len(history) - visLen
	for i := start; i < len(history); i++ {
		normalized := history[i] / maxVal
		idx := int(normalized * 7)
		if idx < 0 {
			idx = 0
		}
		if idx > 7 {
			idx = 7
		}
		sb.WriteRune(sparkBlocks[idx])
	}
	return sparkStyle.Render(sb.String())
}

// renderBar renders a horizontal progress bar with the given percentage.
func renderBar(percent float64, maxWidth int, color lipgloss.Color) string {
	barWidth := maxWidth
	if barWidth < 4 {
		barWidth = 4
	}

	filled := int(math.Round(percent / 100.0 * float64(barWidth)))
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	filledStyle := lipgloss.NewStyle().Foreground(color)
	emptyStyle := lipgloss.NewStyle().Foreground(crt.muted)

	return filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty))
}

// percentColor returns a color based on usage percentage (green → yellow → red).
func percentColor(pct float64) lipgloss.Color {
	switch {
	case pct >= 90:
		return lipgloss.Color("#f7768e") // red/coral
	case pct >= 70:
		return lipgloss.Color("#e0af68") // gold/yellow
	case pct >= 50:
		return lipgloss.Color("#ff9e64") // orange
	default:
		return lipgloss.Color("#9ece6a") // green
	}
}

// tempPercentColor maps a temperature in °C to a status color.
// Thresholds match typical CPU thermal zones: green <60, orange <75, gold <90, red ≥90.
func tempPercentColor(tempC float64) lipgloss.Color {
	switch {
	case tempC >= 90:
		return lipgloss.Color("#f7768e")
	case tempC >= 75:
		return lipgloss.Color("#e0af68")
	case tempC >= 60:
		return lipgloss.Color("#ff9e64")
	default:
		return lipgloss.Color("#9ece6a")
	}
}

// formatBytes formats bytes into human-readable form.
func formatBytes(b uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1fT", float64(b)/float64(tb))
	case b >= gb:
		return fmt.Sprintf("%.1fG", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.0fM", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.0fK", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// formatRate formats bytes/s into a human-readable rate.
func formatRate(bytesPerSec float64) string {
	const (
		kb = 1024.0
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytesPerSec >= gb:
		return fmt.Sprintf("%.1fGB/s", bytesPerSec/gb)
	case bytesPerSec >= mb:
		return fmt.Sprintf("%.1fMB/s", bytesPerSec/mb)
	case bytesPerSec >= kb:
		return fmt.Sprintf("%.0fKB/s", bytesPerSec/kb)
	default:
		return fmt.Sprintf("%.0fB/s", bytesPerSec)
	}
}

// formatHostUptime formats system uptime in days/hours/minutes.
func formatHostUptime(secs uint64) string {
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("up %dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("up %dh %dm", hours, mins)
	default:
		return fmt.Sprintf("up %dm", mins)
	}
}

// readCPUTemperature returns (average, max) CPU/SoC temperature in °C.
// Returns (0, 0) when no usable sensor data is available — common when the
// process lacks SMC access (macOS without root) or no thermal_zone exists.
// On Apple Silicon, PMU* sensors are filtered to focus on CPU/SoC dies.
func readCPUTemperature() (float64, float64) {
	stats, err := sensors.SensorsTemperatures()
	if err != nil || len(stats) == 0 {
		return 0, 0
	}
	var sum float64
	var count int
	var peak float64
	for _, s := range stats {
		if s.Temperature <= 0 || s.Temperature > 150 {
			continue
		}
		if !isCPUTempSensor(s.SensorKey) {
			continue
		}
		sum += s.Temperature
		count++
		if s.Temperature > peak {
			peak = s.Temperature
		}
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), peak
}

func isCPUTempSensor(key string) bool {
	lower := strings.ToLower(key)
	switch {
	case strings.HasPrefix(lower, "pmu tdie"), strings.HasPrefix(lower, "pmu2 tdie"):
		return true // Apple Silicon CPU/GPU dies
	case strings.HasPrefix(lower, "coretemp"), strings.HasPrefix(lower, "k10temp"):
		return true // Linux Intel/AMD CPU
	case strings.HasPrefix(lower, "cpu_thermal"), strings.Contains(lower, "package"):
		return true // generic CPU package
	case strings.Contains(lower, "tctl"), strings.Contains(lower, "tdie"):
		return true // AMD ryzen
	}
	return false
}
