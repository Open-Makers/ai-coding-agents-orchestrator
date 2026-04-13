package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestNewSysmon(t *testing.T) {
	sm := NewSysmon()
	if len(sm.cpuHistory) != sparklineLength {
		t.Errorf("cpuHistory length = %d, want %d", len(sm.cpuHistory), sparklineLength)
	}
	if len(sm.netRecvHist) != sparklineLength {
		t.Errorf("netRecvHist length = %d, want %d", len(sm.netRecvHist), sparklineLength)
	}
	if len(sm.netSentHist) != sparklineLength {
		t.Errorf("netSentHist length = %d, want %d", len(sm.netSentHist), sparklineLength)
	}
	if sm.startTime.IsZero() {
		t.Error("startTime should not be zero")
	}
}

func TestSysmonUpdate(t *testing.T) {
	sm := NewSysmon()
	msg := sysmonTickMsg{
		cpuPercent:  42.5,
		perCore:     []float64{50.0, 35.0, 40.0, 45.0},
		memPercent:  78.3,
		memUsed:     8 * 1024 * 1024 * 1024,
		memTotal:    16 * 1024 * 1024 * 1024,
		memAvail:    4 * 1024 * 1024 * 1024,
		memCached:   2 * 1024 * 1024 * 1024,
		swapPercent: 30.0,
		swapUsed:    1 * 1024 * 1024 * 1024,
		swapTotal:   4 * 1024 * 1024 * 1024,
		diskPercent: 61.2,
		diskUsed:    500 * 1024 * 1024 * 1024,
		diskTotal:   1024 * 1024 * 1024 * 1024,
		netRecv:     1000000,
		netSent:     500000,
		goroutines:  47,
		hostUptime:  86400 + 3600,
		loadAvg:     [3]float64{2.5, 1.8, 1.2},
	}

	updated, cmd := sm.Update(msg)
	if updated.cpuPercent != 42.5 {
		t.Errorf("cpuPercent = %f, want 42.5", updated.cpuPercent)
	}
	if updated.memPercent != 78.3 {
		t.Errorf("memPercent = %f, want 78.3", updated.memPercent)
	}
	if updated.goroutines != 47 {
		t.Errorf("goroutines = %d, want 47", updated.goroutines)
	}
	if cmd == nil {
		t.Error("expected a tick command after update")
	}

	// CPU history should have the new value at the end.
	lastCPU := updated.cpuHistory[len(updated.cpuHistory)-1]
	if lastCPU != 42.5 {
		t.Errorf("last cpuHistory = %f, want 42.5", lastCPU)
	}
}

func TestSysmonView(t *testing.T) {
	sm := NewSysmon()
	sm.cpuPercent = 55.0
	sm.perCore = []float64{60.0, 45.0, 70.0, 30.0}
	sm.memPercent = 70.0
	sm.memUsed = 8 * 1024 * 1024 * 1024
	sm.memTotal = 16 * 1024 * 1024 * 1024
	sm.memAvail = 4 * 1024 * 1024 * 1024
	sm.memCached = 2 * 1024 * 1024 * 1024
	sm.swapPercent = 25.0
	sm.swapUsed = 1 * 1024 * 1024 * 1024
	sm.swapTotal = 4 * 1024 * 1024 * 1024
	sm.diskPercent = 45.0
	sm.diskUsed = 450 * 1024 * 1024 * 1024
	sm.diskTotal = 1024 * 1024 * 1024 * 1024
	sm.goroutines = 30
	sm.recvRate = 2048
	sm.sentRate = 512
	sm.hostUptime = 90000
	sm.loadAvg = [3]float64{2.1, 1.5, 1.0}
	sm.topProcs = []topProcess{
		{pid: 100, name: "orchestrator", cpu: 12.5, mem: 3.2},
		{pid: 200, name: "ollama", cpu: 8.0, mem: 45.0},
	}
	sm.SetSize(44, 60)

	view := sm.View()
	checks := []string{
		"SYSTEM MONITOR", "CPU", "MEM", "DSK", "NET", "PROC",
		"Goroutines", "Session", "C0", "C1",
		"orchestrator", "ollama",
		"Avail", "Cache", "Swap", "Free",
	}
	for _, check := range checks {
		if !strings.Contains(view, check) {
			t.Errorf("view should contain %q", check)
		}
	}
}

func TestRenderSparkline(t *testing.T) {
	history := []float64{0, 25, 50, 75, 100, 50, 25, 0}
	result := renderSparkline(history, 20)
	if result == "" {
		t.Error("sparkline should not be empty")
	}
}

func TestRenderBrailleGraph(t *testing.T) {
	history := []float64{0, 10, 30, 50, 80, 100, 60, 20}
	result := renderBrailleGraph(history, 10, 2, lipgloss.Color("#9ece6a"), 100.0)
	if result == "" {
		t.Error("braille graph should not be empty")
	}
	graphLines := strings.Split(result, "\n")
	if len(graphLines) != 2 {
		t.Errorf("braille graph with 2 rows should have 2 lines, got %d", len(graphLines))
	}
}

func TestRenderBrailleGraphAutoMax(t *testing.T) {
	history := []float64{0, 500, 1000, 2000, 1500}
	result := renderBrailleGraph(history, 8, 2, lipgloss.Color("#73daca"), 0)
	if result == "" {
		t.Error("braille graph with auto-max should not be empty")
	}
}

func TestRenderBrailleGraphAllZero(t *testing.T) {
	history := []float64{0, 0, 0, 0}
	result := renderBrailleGraph(history, 5, 1, lipgloss.Color("#7aa2f7"), 0)
	if result == "" {
		t.Error("braille graph with zero values should not be empty")
	}
}

func TestRenderGradientBar(t *testing.T) {
	result := renderGradientBar(75.0, 20)
	if result == "" {
		t.Error("gradient bar should not be empty")
	}
}

func TestRenderGradientBarEdgeCases(t *testing.T) {
	zeroBar := renderGradientBar(0, 20)
	if zeroBar == "" {
		t.Error("gradient bar at 0% should not be empty")
	}
	fullBar := renderGradientBar(100, 20)
	if fullBar == "" {
		t.Error("gradient bar at 100% should not be empty")
	}
}

func TestRenderBar(t *testing.T) {
	result := renderBar(50.0, 20, lipgloss.Color("#9ece6a"))
	if result == "" {
		t.Error("bar should not be empty")
	}
}

func TestPercentColor(t *testing.T) {
	tests := []struct {
		pct      float64
		expected lipgloss.Color
	}{
		{10.0, lipgloss.Color("#9ece6a")},
		{55.0, lipgloss.Color("#ff9e64")},
		{75.0, lipgloss.Color("#e0af68")},
		{95.0, lipgloss.Color("#f7768e")},
	}
	for _, tt := range tests {
		got := percentColor(tt.pct)
		if got != tt.expected {
			t.Errorf("percentColor(%f) = %s, want %s", tt.pct, got, tt.expected)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    uint64
		expected string
	}{
		{500, "500B"},
		{2048, "2K"},
		{5 * 1024 * 1024, "5M"},
		{8 * 1024 * 1024 * 1024, "8.0G"},
		{2 * 1024 * 1024 * 1024 * 1024, "2.0T"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatRate(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{500, "500B/s"},
		{2048, "2KB/s"},
		{5 * 1024 * 1024, "5.0MB/s"},
	}
	for _, tt := range tests {
		got := formatRate(tt.input)
		if got != tt.expected {
			t.Errorf("formatRate(%f) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatHostUptime(t *testing.T) {
	tests := []struct {
		secs     uint64
		expected string
	}{
		{300, "up 5m"},
		{3700, "up 1h 1m"},
		{90000, "up 1d 1h"},
		{259200, "up 3d 0h"},
	}
	for _, tt := range tests {
		got := formatHostUptime(tt.secs)
		if got != tt.expected {
			t.Errorf("formatHostUptime(%d) = %q, want %q", tt.secs, got, tt.expected)
		}
	}
}

func TestRenderCoreDotted(t *testing.T) {
	history := []float64{10, 20, 30, 50, 70, 90, 80, 60, 40, 20}
	result := renderCoreDotted(0, 50.0, history, 18)
	if result == "" {
		t.Error("renderCoreDotted should not be empty")
	}
	if !strings.Contains(result, "C0") {
		t.Error("renderCoreDotted should contain core label")
	}
	if !strings.Contains(result, "50%") {
		t.Error("renderCoreDotted should contain percentage")
	}
}

func TestRenderBrailleDotBar(t *testing.T) {
	history := []float64{0, 25, 50, 75, 100, 50, 25, 0}
	result := renderBrailleDotBar(history, 10, lipgloss.Color("#9ece6a"))
	if result == "" {
		t.Error("braille dot bar should not be empty")
	}
}

func TestRenderBrailleDotBarEmpty(t *testing.T) {
	result := renderBrailleDotBar(nil, 10, lipgloss.Color("#9ece6a"))
	if result == "" {
		t.Error("braille dot bar with nil history should not be empty")
	}
}

func TestRenderSegmentedBar(t *testing.T) {
	result := renderSegmentedBar(75.0, 20)
	if result == "" {
		t.Error("segmented bar should not be empty")
	}
}

func TestRenderSegmentedBarEdgeCases(t *testing.T) {
	zeroBar := renderSegmentedBar(0, 20)
	if zeroBar == "" {
		t.Error("segmented bar at 0% should not be empty")
	}
	fullBar := renderSegmentedBar(100, 20)
	if fullBar == "" {
		t.Error("segmented bar at 100% should not be empty")
	}
}
