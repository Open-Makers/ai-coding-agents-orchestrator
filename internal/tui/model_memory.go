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
	sampleCfg  config.AgentConfig // representative agent for the live RAM→context preview
	width      int
	height     int
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
		mode:       mode,
		value:      ti,
		totalRAMGB: total,
		sampleCfg:  representativeAgent(cfg.Agents),
		width:      80,
		height:     24,
	}
	m.value.SetValue(m.initialValue(cfg))
	return m
}

// representativeAgent picks a local agent config to drive the RAM→context
// preview. The coder is preferred (it does the heaviest work); otherwise any
// local agent, falling back to a zero config.
func representativeAgent(agents map[string]config.AgentConfig) config.AgentConfig {
	if ac, ok := agents["coder"]; ok && runner.IsLocalRunner(ac) {
		return ac
	}
	for _, ac := range agents {
		if runner.IsLocalRunner(ac) {
			return ac
		}
	}
	if ac, ok := agents["coder"]; ok {
		return ac
	}
	return config.AgentConfig{}
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

func (m modelMemoryModel) Init() tea.Cmd { return textinput.Blink }

func (m modelMemoryModel) Update(msg tea.Msg) (modelMemoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
		b.WriteString(dimStyle.Render("  " + m.preview()))
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

// preview shows the estimated context window the RAM limit yields for the
// representative agent, so the user sees the effect before saving.
func (m modelMemoryModel) preview() string {
	gb, err := strconv.ParseFloat(strings.TrimSpace(m.value.Value()), 64)
	if err != nil || gb <= 0 {
		return "Enter a RAM budget to preview the derived context size."
	}
	tokens := runner.ContextTokensForRAM(m.sampleCfg, gb)
	model := m.sampleCfg.Model
	if model == "" {
		model = "the configured model"
	}
	return fmt.Sprintf("≈ %d-token context for %s (auto, per agent).", tokens, model)
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
