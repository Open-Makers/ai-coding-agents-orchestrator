# TUI Reference

The orchestrator ships with a full-screen terminal user interface (TUI). This
document explains every panel and indicator you will see, with concrete
examples.

The screen is split into three areas:

1. **Agent panels** — one panel per active agent, showing live streaming output.
2. **Status bar** — a single line at the bottom with run state, stage progress,
   runner/model, elapsed time, and keyboard shortcuts.
3. **System Monitor** — an optional side panel (toggle with `Ctrl+T`) showing
   CPU, memory, network, and token usage.

---

## Agent panels

Each agent renders a panel with a header and a scrolling body of streamed
output. The header shows the agent role and its current status:

```
[CODER]  ⠹ RUNNING
```

| Indicator     | Meaning                                                        |
|---------------|----------------------------------------------------------------|
| `○ WAITING`   | Agent is queued and has not started yet.                       |
| `⠋ RUNNING`   | Agent is actively producing output (animated spinner).         |
| `⠋ FIXING`    | Agent is re-running to fix issues found by a quality gate.     |
| `✓ DONE`      | Agent finished successfully.                                    |
| `✗ ERROR`     | Agent failed (e.g. build error, runner crash, quota/rate limit).|
| `⏸ GATE`      | Agent output is waiting for your approval before continuing.   |

The body streams tokens from the model as they arrive. When a panel has more
output than fits on screen, a scroll indicator appears; use `↑`/`↓` to scroll.

---

## Status bar

The bottom line is laid out as:

```
main  ● coder   ▸ Stage 2/5: Must Have — Auth        copilot/claude-opus-4.8  ⏱ 3m 12s   ↑↓ scroll  Ctrl+R req  …  v0.9.0
```

Reading left to right:

| Segment                         | Example                                  | Meaning                                                                 |
|---------------------------------|------------------------------------------|-------------------------------------------------------------------------|
| **Git branch**                  | `main`                                   | The current branch of the project.                                      |
| **State**                       | `● coder`, `⏸ … — waiting for approval`  | What the pipeline is doing right now (running agent, gate, done, error).|
| **Stage info**                  | `▸ Stage 2/5: Must Have — Auth`          | Current implementation stage; scrolls as a marquee if it is too long.   |
| **Runner / model**              | `copilot/claude-opus-4.8`                | The active runner and model for the running agent.                      |
| **Elapsed timer**               | `⏱ 3m 12s`                               | Time since coding started (appears once the Coder begins).              |
| **Keyboard shortcuts**          | `↑↓ scroll  Ctrl+R req  …`               | Available actions (see below).                                          |
| **Version**                     | `v0.9.0`                                 | The orchestrator version.                                               |

### Keyboard shortcuts

| Key        | Action                                                       |
|------------|--------------------------------------------------------------|
| `↑` / `↓`  | Scroll the focused panel.                                     |
| `Ctrl+R`   | Open the requirements file.                                   |
| `Ctrl+G`   | Open the git diff view.                                       |
| `Ctrl+C`   | Open the PM chat / requirements conversation.                 |
| `Ctrl+T`   | Toggle the System Monitor panel.                              |
| `Ctrl+A`   | Approve the current human-approval gate.                      |
| `Ctrl+E`   | Toggle the error banner.                                      |
| `Ctrl+X`   | Cancel the current run.                                       |
| `q`        | Quit.                                                         |

---

## System Monitor (`Ctrl+T`)

The System Monitor shows live host metrics and, most importantly, per-agent
token usage. It has four sections: **CPU**, **MEM**, **NET**, and **TOKENS**.

```
⚡ SYSTEM MONITOR
  up 4h 12m • 10 cores

── CPU ──
  34.2%   load: 2.1 1.8 1.6

── MEM ──
  61.0%   12.4 GB / 32.0 GB

── NET ──
  ▼ 1.2 MB/s   ▲ 240 KB/s

── TOKENS ──
 Total: 75.6k tok
 pm          ↓11.9k ↑2.2k~
 coder       ↓33.7k ↑4.5k~
 qa          ↓27.0k ↑1.8k~
```

### NET vs. TOKENS arrows

Note that the two sections use **different** glyphs:

- **NET** uses solid triangles for network throughput:
  - `▼` = **download** rate (bytes received per second).
  - `▲` = **upload** rate (bytes sent per second).
- **TOKENS** uses thin arrows for token counts:
  - `↓` = **input** tokens (the prompt the agent sent *to* the model).
  - `↑` = **output** tokens (the text the model generated *back*).

### Reading the TOKENS section

- **`Total: 75.6k tok`** — the sum of input + output tokens across all agents
  for this run.
- Each following line is one agent: its role, then `↓` input tokens and `↑`
  output tokens.
- A trailing **`~`** means the counts are **estimated** locally rather than
  reported exactly by the CLI. This is normal for local models and fallback
  runners that do not return precise usage; runners that report exact usage
  (e.g. cloud APIs) omit the `~`.

**Worked example:**

```
 coder       ↓33.7k ↑4.5k~
```

> The `coder` agent consumed roughly **33,700 input tokens** (the prompts,
> context, and code it fed to the model) and produced roughly **4,500 output
> tokens** (the code and explanations it generated). The `~` indicates these
> figures are estimated.

```
 pm          ↓11.9k ↑2.2k
```

> The `pm` agent used **exactly 11,900 input** and **2,200 output** tokens —
> no `~`, so these came directly from the runner's reported usage.

### Number formatting

Token and rate values are abbreviated for compactness:

| Raw value     | Displayed |
|---------------|-----------|
| `950`         | `950`     |
| `2,200`       | `2.2k`    |
| `33,700`      | `33.7k`   |
| `1,250,000`   | `1.3M`    |

When more agents are active than fit in the section, the list auto-scrolls
through them.
