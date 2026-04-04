# Implementation Plan

## Przegląd faz

| Faza | Nazwa | Cel | Zależności |
|---|---|---|---|
| 1 | Foundation | Bus, Agent interface, Runner abstraction, Config | — |
| 2 | Agent Migration | Przepisanie logiki orchestratora na agenty | Faza 1 |
| 3 | Anthropic Runner | Claude API z streamingiem i skill loaderem | Faza 1 |
| 4 | TUI Core | Bubble Tea, Agent Panels, Conversation Panel | Faza 2 |
| 5 | TUI Interactions | Picker, Editor, Git Panel, Chat | Faza 4 |
| 6 | Parallelism | Równoległe Tester + Reviewer | Faza 2 |
| 7 | tmux Mode | Multiplexer, tryb --ui=tmux | Faza 4 |

Fazy 1–4 są sekwencyjne. Fazy 5, 6, 7 można rozwijać równolegle po Fazie 4.

---

## Faza 1 — Foundation

**Cel:** Nowe pakiety szkieletowe bez żadnych zmian w istniejącej logice. Kod kompiluje się i przechodzi testy po każdym commicie.

### 1.1 Project Config (`internal/config`)

Nowe pliki:
```
internal/config/
  config.go      -- struct Config + loader
  defaults.go    -- wartości domyślne
```

Struktura:
```go
type Config struct {
    Project ProjectConfig
    Agents  map[string]AgentConfig
}

type ProjectConfig struct {
    Name     string
    Language string
    TestCmd  string
    LintCmd  string
    Scope    ScopeConfig
    Context  ContextConfig // always_include files
}

type AgentConfig struct {
    Runner string
    Model  string
    Skills []string
}
```

Loader szuka `.orchestrator.yaml` w CWD. Brak pliku = defaults (language: go, test: `go test ./...`).

Done: `config.Load(".")` zwraca sensowne defaults gdy brak pliku.

---

### 1.2 Message Bus (`internal/bus`)

Nowe pliki:
```
internal/bus/
  types.go    -- Message, MessageType, AgentRole, Event
  bus.go      -- Bus struct, Publish, Subscribe, ring-buffer
```

```go
type MessageType string
const (
    MsgRequest   MessageType = "request"
    MsgResponse  MessageType = "response"
    MsgEvent     MessageType = "event"
    MsgHumanGate MessageType = "human_gate"
)

type Message struct {
    ID        string
    From      AgentRole
    To        AgentRole  // "" = broadcast
    Type      MessageType
    Payload   any
    Timestamp time.Time
}

type Bus struct {
    mu   sync.RWMutex
    ring []Message     // ring buffer, rozmiar konfigurowalny
    head int
    subs []chan Message // subskrybenci (TUI, logger)
}

func (b *Bus) Publish(m Message)
func (b *Bus) Subscribe() <-chan Message
func (b *Bus) Recent(n int) []Message
```

Persist: każda wiadomość dopisywana do `.orchestrator/runlog.jsonl`.

Done: unit testy — publish/subscribe, ring-buffer overflow, concurrent access.

---

### 1.3 Agent Interface (`internal/agent`)

Nowe pliki:
```
internal/agent/
  agent.go    -- interfejs Agent, AgentRole constants
  base.go     -- BaseAgent struct (wspólne pola: bus, runner, config)
```

```go
type AgentRole string
const (
    RolePlanner  AgentRole = "planner"
    RoleCoder    AgentRole = "coder"
    RoleTester   AgentRole = "tester"
    RoleReviewer AgentRole = "reviewer"
    RoleFixer    AgentRole = "fixer"
    RolePR       AgentRole = "pr"
)

type Agent interface {
    Role() AgentRole
    Run(ctx context.Context, msg bus.Message) (bus.Message, error)
}

type BaseAgent struct {
    role   AgentRole
    bus    *bus.Bus
    runner runner.LLMRunner
    cfg    config.AgentConfig
}
```

Done: interfejs kompiluje się, `BaseAgent` implementuje pomocnicze metody `emit()` i `request()`.

---

### 1.4 Runner Interface (`internal/runner`)

Zmiany w istniejącym pakiecie:

```
internal/runner/
  runner.go      -- interfejs LLMRunner (nowy)
  codex.go       -- istniejący CodexRunner, dostosowany do interfejsu
  mock.go        -- MockRunner do testów
```

```go
type Token struct {
    Text  string
    Done  bool
    Error error
}

type CompletionRequest struct {
    SystemPrompt string
    Skills       []string
    Messages     []ConvMessage
    Model        string
}

type LLMRunner interface {
    Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error)
}
```

`MockRunner` zwraca zaprogramowane odpowiedzi — używany we wszystkich testach od tej pory.

Done: istniejące testy przechodzą, `CodexRunner` implementuje `LLMRunner`.

---

## Faza 2 — Agent Migration

**Cel:** Każda faza pipeline'u staje się osobnym `Agent`. Orchestrator to pętla event-driven. Codex CLI nadal działa jako domyślny runner.

### 2.1 Agenty (jeden per faza)

Nowe pliki:
```
internal/agent/
  planner.go
  coder.go
  tester.go
  reviewer.go
  fixer.go
  pr.go
```

Każdy agent:
1. Dostaje `bus.Message` z payloadem odpowiednim dla roli.
2. Wywołuje `runner.Complete()` z własnym system promptem.
3. Konsumuje `<-chan Token` i streamuje tokeny jako `MsgEvent` na Bus.
4. Po zakończeniu zapisuje artefakt do `.orchestrator/` (zachowana kompatybilność).
5. Publikuje `MsgResponse` z wynikiem.

Przeniesienie logiki:
- `PlannerAgent` ← PLAN z `internal/orchestrator/orchestrator.go`
- `CoderAgent` ← CODE
- `TesterAgent` ← TEST + `internal/orchestrator/tester.go`
- `ReviewerAgent` ← REVIEW + `internal/orchestrator/review_parser.go`
- `FixerAgent` ← FIX
- `PRAgent` ← DONE

Done: każdy agent ma test z MockRunner sprawdzający poprawność artefaktu wyjściowego.

---

### 2.2 Orchestrator — pętla event-driven

Plik: `internal/orchestrator/orchestrator.go` (przepisany)

```go
type PipelineState string
const (
    StateIdle      PipelineState = "idle"
    StatePlanning  PipelineState = "planning"
    StateCoding    PipelineState = "coding"
    StateTesting   PipelineState = "testing"
    StateReviewing PipelineState = "reviewing"
    StateFixing    PipelineState = "fixing"
    StateDone      PipelineState = "done"
    StateGate      PipelineState = "human_gate"
)

type Orchestrator struct {
    bus    *bus.Bus
    agents map[agent.AgentRole]agent.Agent
    cfg    config.Config
    state  PipelineState
}
```

Project Context Collector (`internal/context/collector.go`):
```go
type ProjectContext struct {
    Files         []string // git ls-files
    RecentCommits []string // git log --oneline -20
    UnstagedDiff  string   // git diff HEAD
    Config        config.Config
}

func Collect(root string, cfg config.Config) (ProjectContext, error)
```

Done: `orchestrator run --requirements x.md` produkuje te same artefakty co przed refaktorem.

---

### 2.3 CLI update

`cmd/orchestrator/main.go`:
- `runCmd` tworzy Bus, ładuje Config, inicjuje agenty przez factory, przekazuje do `Orchestrator.Run`
- `--ui=plain` (domyślny na tym etapie) — Bus subskrybuje prosty logger do stdout
- usunięcie bezpośrednich wywołań `runner.CodexRunner{}` z main.go

Done: `orchestrator run`, `report`, `approve`, `clean` działają jak przed refaktorem.

---

## Faza 3 — Anthropic Runner

**Cel:** `AnthropicRunner` z Claude API, streaming SSE, skill loader. Agenty mogą używać Claude przez zmianę konfiguracji.

### 3.1 Skill Loader (`internal/skills`)

```
internal/skills/
  loader.go    -- SkillLoader.Load(name) string
  cache.go     -- lokalny cache ~/.config/orchestrator/skills/
```

Loader:
1. Sprawdza `~/.config/orchestrator/skills/<name>.md`
2. Jeśli brak — pobiera z GitHub raw (`affaan-m/everything-claude-code`)
3. Zapisuje do cache
4. Przy starcie orchestratora: pre-fetch wszystkich skillów zadeklarowanych w `agents.yaml`

Done: `skills.Load("golang-patterns")` działa offline po pierwszym pobraniu.

---

### 3.2 Anthropic Runner (`internal/runner/anthropic.go`)

```go
type AnthropicRunner struct {
    apiKey      string
    skillLoader *skills.Loader
    httpClient  *http.Client
}

func (r *AnthropicRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
    systemPrompt := r.buildSystemPrompt(req.SystemPrompt, req.Skills)
    // POST https://api.anthropic.com/v1/messages (stream: true)
    // parsuj SSE: event: content_block_delta → Token{Text: delta.text}
    // event: message_stop → Token{Done: true}
}
```

Konfiguracja: `ANTHROPIC_API_KEY` z env lub `~/.config/orchestrator/credentials.yaml`.

Done: `AnthropicRunner` streamuje tokeny z Claude API. Testy z httptest mockującym SSE endpoint.

---

### 3.3 Runner Factory (`internal/runner/factory.go`)

```go
func New(cfg config.AgentConfig, skillLoader *skills.Loader) (LLMRunner, error) {
    switch cfg.Runner {
    case "claude":
        return NewAnthropicRunner(os.Getenv("ANTHROPIC_API_KEY"), skillLoader)
    case "codex":
        return NewCodexRunner()
    default:
        return nil, fmt.Errorf("unknown runner: %s", cfg.Runner)
    }
}
```

Done: `runner: codex` → `runner: claude` w `.orchestrator.yaml` przełącza runner bez zmian w kodzie agentów.

---

## Faza 4 — TUI Core

**Cel:** Bubble Tea zastępuje plain stdout. Agent Panels + Conversation Panel. Pipeline działa niezmiennie.

### 4.1 Zależności

```bash
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/bubbles
go get github.com/charmbracelet/lipgloss
```

---

### 4.2 Model główny (`internal/tui/model.go`)

```go
type Model struct {
    agents    map[agent.AgentRole]*AgentPanelModel
    conv      ConversationModel
    statusbar StatusBarModel
    overlay   tea.Model        // nil lub aktywny overlay
    bus       *bus.Bus
    events    <-chan bus.Message
    width     int
    height    int
}
```

Bridge Bus → TUI: goroutine czyta `bus.Subscribe()` i wysyła `tea.Msg` przez `tea.Program.Send()`.

---

### 4.3 Agent Panel (`internal/tui/agent_panel.go`)

```
┌─────────────────────────┐
│  Planner  ✓ done        │
│  plan.json ready        │
│  3 steps, 2 files       │
└─────────────────────────┘
```

Stany i kolory (lipgloss):
- `waiting` — szary
- `running` — niebieski + spinner (`charmbracelet/bubbles/spinner`)
- `done` — zielony ✓
- `error` — czerwony ✗
- `gate` — żółty, czeka na approve

---

### 4.4 Conversation Panel (`internal/tui/conversation.go`)

`charmbracelet/bubbles/viewport` z ring-bufferem wiadomości z Busa.

Format wiersza:
```
14:02:31  planner → coder    plan.json ready (3 steps)
14:03:15  coder   → bus      streaming patch… [████████░░] 80%
14:05:02  coder   → tester   patch.diff ready
```

Kolory per rola: Planner=cyan, Coder=blue, Tester=yellow, Reviewer=magenta, Fixer=red.

---

### 4.5 Status Bar (`internal/tui/statusbar.go`)

```
feat/reset-password  ●  Coding  │  Ctrl+R req  Ctrl+G git  Ctrl+C chat  Ctrl+A approve  q quit
```

---

### 4.6 Integracja z CLI

```go
// cmd/orchestrator/main.go — runCmd
if cfg.UI == "tui" {
    model := tui.New(orch.Bus(), agentRoles)
    p := tea.NewProgram(model, tea.WithAltScreen())
    go orch.Run(ctx, requirementsPath)  // pipeline w osobnej goroutine
    p.Run()
}
```

`--ui=tui` = domyślny. `--ui=plain` zachowany dla CI.

Done: `orchestrator run` pokazuje TUI z panelami, tokeny streamowane na żywo.

---

## Faza 5 — TUI Interactions

**Cel:** Pełna interakcja użytkownika — ekran startowy, edytor, Git Panel, Chat.

### 5.1 Ekran startowy + Picker (`internal/tui/picker.go`)

Wyświetlany gdy brak `--requirements`:
```
┌─────────────────────────────────────────────────────────────────┐
│  orchestrator  ·  select requirements                           │
├─────────────────────────────────────────────────────────────────┤
│  ► New — open editor                                            │
│    Pick file from repo…                                         │
│    Recent: tasks/add-reset-password.md          2026-04-03      │
│    Recent: tasks/refactor-auth.md               2026-04-01      │
└─────────────────────────────────────────────────────────────────┘
│  ↑↓ navigate   Enter select   q quit                            │
└─────────────────────────────────────────────────────────────────┘
```

File picker (`internal/tui/filetree.go`):
- lista MD z `git ls-files`, fuzzy filter
- `Ctrl+P` — split preview bez wychodzenia z pickera

Historia ostatnich użyć: `.orchestrator/history.json`.

Done: `orchestrator` (bez flag) otwiera picker, wybór pliku startuje pipeline.

---

### 5.2 Edytor wymagań (`internal/tui/editor.go`)

`charmbracelet/bubbles/textarea` jako pełnoekranowy overlay:
- `Ctrl+S` — zapisuje `.orchestrator/requirements.md`, startuje/restartuje pipeline
- `Ctrl+E` — `exec $EDITOR tmpfile`, po powrocie wczytuje treść
- dostępny przez `Ctrl+R` gdy pipeline w HumanGate (re-plan od PlannerAgent)

Done: user pisze wymagania w TUI bez opuszczania aplikacji.

---

### 5.3 Git Panel (`internal/tui/git.go` + `internal/gitclient`)

`internal/gitclient/gitclient.go` — wrapper na `exec git`:
```go
type GitClient struct { root string }

func (g *GitClient) Status() ([]FileStatus, error)    // git status --porcelain
func (g *GitClient) Diff(path string) (string, error)  // git diff HEAD -- <path>
func (g *GitClient) Stage(path string) error           // git add <path>
func (g *GitClient) Unstage(path string) error         // git restore --staged <path>
func (g *GitClient) Commit(msg string) error           // git commit -m
func (g *GitClient) ResetFile(path string) error       // git checkout HEAD -- <path>
func (g *GitClient) Log(base string) ([]Commit, error) // git log <base>..HEAD --oneline
```

Panel TUI:
```
┌──────────────────┬──────────────────────────────────────────────┐
│  Staged (2)      │  Diff                                        │
│  ► M internal/   │  --- a/internal/user/service.go              │
│    M cmd/main.go │  +++ b/internal/user/service.go              │
│                  │  @@ -45,6 +45,18 @@                          │
│  Unstaged (1)    │  +func (s *UserService) ResetPassword(       │
│    M go.sum      │  +    ...                                     │
│  Commits (3)     │                                              │
│  · feat: add re… │                                              │
└──────────────────┴──────────────────────────────────────────────┘
│  a  stage/unstage   c  commit   r  reset file   Tab  switch pane│
└─────────────────────────────────────────────────────────────────┘
```

Syntax highlight diffa: `github.com/alecthomas/chroma/v2`.

Integracja z pipeline:
- Bus emituje `EventPatchApplied` → Git Panel auto-refresh
- `r` (reset) na pliku zmienionym przez agenta → `FileRejected` event → FIX z notatką

Zakres (celowo brak): push, merge, rebase, cherry-pick, zarządzanie branchami.

Done: `Ctrl+G` otwiera panel, user widzi i może zarządzać zmianami agenta.

---

### 5.4 Live Chat (`internal/tui/chat.go`)

Overlay: `viewport` (historia) + `textarea` (input).

Context builder przy każdym zapytaniu:
```go
func buildChatContext(ws artifacts.Workspace) string {
    // requirements.md + plan.json (skrócony) + ostatnie 50 linii diff + review.md
}
```

- streaming tokenów przez `AnthropicRunner.Complete()`
- historia: `.orchestrator/chat.jsonl`
- chat nie steruje pipeline'em — tylko konwersacja

Done: `Ctrl+C` otwiera chat, Claude odpowiada ze znajomością aktualnego stanu pipeline'u.

---

## Faza 6 — Parallelism

**Cel:** Tester i Reviewer działają równolegle.

### 6.1 Równoległy krok TEST + REVIEW

```go
// internal/orchestrator/orchestrator.go — po zakończeniu Codera
var wg sync.WaitGroup
var testResult, reviewResult bus.Message
var testErr, reviewErr error

wg.Add(2)
go func() {
    defer wg.Done()
    testResult, testErr = o.agents[RoleTester].Run(ctx, codeMsg)
}()
go func() {
    defer wg.Done()
    reviewResult, reviewErr = o.agents[RoleReviewer].Run(ctx, codeMsg)
}()
wg.Wait()

// Oceń oba wyniki razem, zdecyduj o FIX lub DONE
```

TUI: oba panele (Tester, Reviewer) pokazują `running` jednocześnie.

Done: oba agenty produkują artefakty, pipeline nie kontynuuje zanim oba nie skończą.

---

## Faza 7 — tmux Mode

**Cel:** `--ui=tmux` — każdy agent we własnym pane terminala.

### 7.1 Multiplexer Interface (`internal/multiplexer`)

```go
type Multiplexer interface {
    CreateSession(id string) error
    NewPane(role agent.AgentRole) (PaneID, error)
    WriteToPane(id PaneID, text string) error
    Close() error
}
```

### 7.2 TmuxMultiplexer (`internal/multiplexer/tmux.go`)

1. `tmux new-session -d -s orch-<id>`
2. Per agent: `tmux split-window` (lub `new-window`)
3. W każdym pane: `orchestrator agent --role=<role> --session=<id> --bus-addr=<unix-socket>`
4. Główne okno: Conversation Panel (Bubble Tea, bez agent panels)

Sub-command `orchestrator agent` (`cmd/orchestrator/agent_cmd.go`):
- łączy się z Bus przez Unix socket
- uruchamia pojedynczy agent
- streamuje output do własnego pane

Done: `orchestrator run --ui=tmux` tworzy sesję tmux z panes per agent.

---

## Struktura plików po wszystkich fazach

```
cmd/orchestrator/
  main.go           -- CLI entry, factory
  agent_cmd.go      -- F7: sub-process dla tmux pane

internal/
  config/
    config.go       -- F1
    defaults.go     -- F1
  bus/
    types.go        -- F1
    bus.go          -- F1
  agent/
    agent.go        -- F1: interfejs + AgentRole
    base.go         -- F1: BaseAgent
    planner.go      -- F2
    coder.go        -- F2
    tester.go       -- F2
    reviewer.go     -- F2
    fixer.go        -- F2
    pr.go           -- F2
  runner/
    runner.go       -- F1: interfejs LLMRunner
    codex.go        -- F1: istniejący, dostosowany
    mock.go         -- F1
    anthropic.go    -- F3
    factory.go      -- F3
  skills/
    loader.go       -- F3
    cache.go        -- F3
  context/
    collector.go    -- F2: git ls-files, log, diff
  orchestrator/
    orchestrator.go -- F2: przepisany event-driven loop
    policy.go       -- bez zmian
  gitclient/
    gitclient.go    -- F5: exec git wrapper
  tui/
    model.go        -- F4: root Bubble Tea model
    agent_panel.go  -- F4
    conversation.go -- F4
    statusbar.go    -- F4
    picker.go       -- F5
    filetree.go     -- F5
    editor.go       -- F5
    git.go          -- F5
    chat.go         -- F5
  multiplexer/
    multiplexer.go  -- F7
    tmux.go         -- F7
  artifacts/        -- bez zmian
  policy/           -- bez zmian
```

---

## Zależności zewnętrzne do dodania

| Pakiet | Faza | Do czego |
|---|---|---|
| `github.com/charmbracelet/bubbletea` | F4 | TUI framework |
| `github.com/charmbracelet/bubbles` | F4 | viewport, textarea, list, spinner |
| `github.com/charmbracelet/lipgloss` | F4 | style, kolory, layout |
| `github.com/alecthomas/chroma/v2` | F5 | syntax highlight diffa w Git Panelu |
| `gopkg.in/yaml.v3` | F1 | `.orchestrator.yaml` config |

Bez `go-git`, bez `libgit2` — git przez `exec git`.
