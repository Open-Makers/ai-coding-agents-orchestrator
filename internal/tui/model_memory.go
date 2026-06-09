package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// modelMemory mode indices for the on-screen toggle.
const (
	mmModeOff = iota
	mmModeRAM
	mmModeContext
)

var mmModeLabels = []string{"Off", "RAM limit (GB)", "Context limit (tokens)"}
var mmModeKeys = []string{"off", "ram", "context"}

// modelMemoryDoneMsg is emitted when the user saves the model-memory setting.
type modelMemoryDoneMsg struct{ cfg config.ModelMemoryConfig }

// modelMemoryCancelledMsg is emitted when the user cancels without saving.
type modelMemoryCancelledMsg struct{}

// modelMemoryModel is a small standalone screen for the global "Model Memory"
// setting: a toggle between bounding local models by RAM or by context size
// (or off), plus a single numeric value for the selected mode.
type modelMemoryModel struct {
	mode       int
	value      textinput.Model
	totalRAMGB float64

	// Available local models discovered across backends, for the live
	// RAM→context preview. Loaded asynchronously on Init.
	localModels  []runner.LocalModel
	localLoading bool

	width  int
	height int
}

// localModelsMsg carries the result of the async local-model discovery.
type localModelsMsg struct {
	models []runner.LocalModel
}

func fetchLocalModelsCmd() tea.Cmd {
	return func() tea.Msg {
		return localModelsMsg{models: runner.ListLocalModels()}
	}
}

func newModelMemoryModel(cfg config.Config) modelMemoryModel {
	ti := textinput.New()
	ti.CharLimit = 12
	ti.Prompt = ""
	ti.Focus()

	mode := mmModeOff
	switch cfg.ModelMemory.Mode {
	case "ram":
		mode = mmModeRAM
	case "context":
		mode = mmModeContext
	}

	var total float64
	if vm, err := mem.VirtualMemory(); err == nil && vm.Total > 0 {
		total = float64(vm.Total) / (1 << 30)
	}

	m := modelMemoryModel{
		mode:         mode,
		value:        ti,
		totalRAMGB:   total,
		localLoading: true,
		width:        80,
		height:       24,
	}
	m.value.SetValue(m.initialValue(cfg))
	return m
}

func (m modelMemoryModel) initialValue(cfg config.Config) string {
	switch m.mode {
	case mmModeRAM:
		if cfg.ModelMemory.MaxRAMGB > 0 {
			return trimFloat(cfg.ModelMemory.MaxRAMGB)
		}
		if m.totalRAMGB > 0 {
			return trimFloat(roundHalf(m.totalRAMGB * 0.7))
		}
	case mmModeContext:
		if cfg.ModelMemory.MaxContextTokens > 0 {
			return strconv.Itoa(cfg.ModelMemory.MaxContextTokens)
		}
		return "8192"
	}
	return ""
}

func (m modelMemoryModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, fetchLocalModelsCmd())
}

func (m modelMemoryModel) Update(msg tea.Msg) (modelMemoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case localModelsMsg:
		m.localModels = msg.models
		m.localLoading = false
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return modelMemoryCancelledMsg{} }
		case "left", "h":
			m.mode = (m.mode + len(mmModeKeys) - 1) % len(mmModeKeys)
			m.value.SetValue(m.defaultForMode())
			return m, nil
		case "right", "l":
			m.mode = (m.mode + 1) % len(mmModeKeys)
			m.value.SetValue(m.defaultForMode())
			return m, nil
		case "enter", "ctrl+s":
			return m, func() tea.Msg { return modelMemoryDoneMsg{cfg: m.result()} }
		}
	}
	if m.mode == mmModeOff {
		return m, nil
	}
	var cmd tea.Cmd
	m.value, cmd = m.value.Update(msg)
	m.value.SetValue(digitsOnly(m.value.Value(), m.mode == mmModeRAM))
	return m, cmd
}

func (m modelMemoryModel) defaultForMode() string {
	switch m.mode {
	case mmModeRAM:
		if m.totalRAMGB > 0 {
			return trimFloat(roundHalf(m.totalRAMGB * 0.7))
		}
	case mmModeContext:
		return "8192"
	}
	return ""
}

func (m modelMemoryModel) result() config.ModelMemoryConfig {
	cfg := config.ModelMemoryConfig{Mode: mmModeKeys[m.mode]}
	switch m.mode {
	case mmModeRAM:
		if gb, err := strconv.ParseFloat(strings.TrimSpace(m.value.Value()), 64); err == nil {
			cfg.MaxRAMGB = gb
		}
	case mmModeContext:
		if n, err := strconv.Atoi(strings.TrimSpace(m.value.Value())); err == nil {
			cfg.MaxContextTokens = n
		}
	}
	return cfg
}

func (m modelMemoryModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(homePalette.accent)
	dimStyle := lipgloss.NewStyle().Foreground(homePalette.dim)
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(homePalette.green)

	var b strings.Builder
	b.WriteString(titleStyle.Render("🧠 Model Memory"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Cap how much memory local models may use. Choose RAM (auto-converted\nto a per-agent context window) OR a fixed context size. ←/→ toggle mode."))
	b.WriteString("\n\n")

	// Mode toggle row.
	b.WriteString("  Mode:  ")
	for i, label := range mmModeLabels {
		if i == m.mode {
			b.WriteString(activeStyle.Render("[" + label + "]"))
		} else {
			b.WriteString(dimStyle.Render(" " + label + " "))
		}
		if i < len(mmModeLabels)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n\n")

	switch m.mode {
	case mmModeOff:
		b.WriteString(dimStyle.Render("  Local models run with their own default context window."))
	case mmModeRAM:
		b.WriteString("  Max RAM (GB):  ")
		b.WriteString(m.value.View())
		if m.totalRAMGB > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("   (system total: %.1f GB)", m.totalRAMGB)))
		}
		b.WriteString("\n\n")
		b.WriteString(m.renderPreview(dimStyle))
	case mmModeContext:
		b.WriteString("  Max context (tokens):  ")
		b.WriteString(m.value.View())
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("  Applied verbatim to every agent."))
	}

	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  enter/ctrl+s save   ·   esc cancel"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

// renderPreview lists the estimated context window the RAM limit yields for
// every local model currently available across backends, so the user sees the
// effect on each model before saving.
func (m modelMemoryModel) renderPreview(dim lipgloss.Style) string {
	gb, err := strconv.ParseFloat(strings.TrimSpace(m.value.Value()), 64)
	if err != nil || gb <= 0 {
		return dim.Render("  Enter a RAM budget to preview the derived context size.")
	}
	if m.localLoading {
		return dim.Render("  Estimated context per available local model:\n  scanning local backends…")
	}
	if len(m.localModels) == 0 {
		return dim.Render("  No local models detected (Ollama / LM Studio / oMLX not running?).\n  Heuristic for a typical 7B model: ≈ " +
			strconv.Itoa(runner.ContextTokensForWeights(0, gb)) + " tokens.")
	}

	headStyle := lipgloss.NewStyle().Foreground(homePalette.gold)
	valStyle := lipgloss.NewStyle().Foreground(homePalette.green)

	var lines []string
	lines = append(lines, headStyle.Render("  Estimated context per available local model:"))

	// Cap the list so the screen does not overflow; note any remainder.
	maxRows := m.height - 14
	if maxRows < 4 {
		maxRows = 4
	}
	shown := m.localModels
	extra := 0
	if len(shown) > maxRows {
		extra = len(shown) - maxRows
		shown = shown[:maxRows]
	}

	for _, lm := range shown {
		tokens := runner.ContextTokensForWeights(lm.WeightBytes, gb)
		est := ""
		if lm.WeightBytes <= 0 {
			est = dim.Render(" (est)")
		}
		label := fmt.Sprintf("%-9s %s", lm.Runner, lm.Model)
		lines = append(lines, "  "+dim.Render(label)+"  "+
			valStyle.Render(fmt.Sprintf("≈ %d tok", tokens))+est)
	}
	if extra > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("  … and %d more", extra)))
	}
	return strings.Join(lines, "\n")
}

func digitsOnly(s string, allowDot bool) string {
	var b strings.Builder
	seenDot := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if allowDot && r == '.' && !seenDot {
			seenDot = true
			b.WriteRune(r)
		}
	}
	return b.String()
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(f, 'f', 1, 64), "0"), ".")
}

func roundHalf(f float64) float64 {
	return float64(int(f*2+0.5)) / 2
}
