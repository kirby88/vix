# Why I Built vix: A Coding Agent That Goes Beyond Chat

Claude Code, OpenCode, Codex — they all follow the same pattern: one process, one chat loop, read files when asked. I wanted something fundamentally different. Something that understands your project before you type a single prompt. Something that survives a terminal crash. Something that doesn't waste 10 LLM round-trips reading 10 files.

So I built **vix**.

Here's what makes it different.

---

## The Architecture Problem Nobody Talks About

Every coding agent today runs as a monolith. The LLM loop, the tools, the UI — all living in one process. Kill the terminal? Lose everything. Want to reconnect from another machine? Start over.

vix splits into two processes: a **persistent daemon** and a **lightweight TUI client**, connected via Unix socket.

The daemon runs your LLM calls, tools, and project analysis. The client is pure rendering. This means:

- **Hot-reload the UI** without losing agent state or conversation
- **Multiple clients** can connect to the same session
- **Daemon survives terminal crashes** — no lost work
- Clean separation: the UI never touches your files, the daemon never renders pixels

It sounds simple, but it changes everything about reliability.

---

## The Brain: Your Project, Pre-Understood

Here's what happens when you start a conversation with Claude Code: "Let me explore the codebase first." Then it reads `README.md`. Then `package.json`. Then the file you actually care about. Three turns burned on orientation.

vix has a **Brain** — a two-phase analysis engine that runs when your project initializes:

**Phase 1 — Static Analysis:**
- Scans all files with language detection and `.gitignore` filtering
- Extracts symbols via LSP (gopls, pylsp, tsserver)
- Builds an import dependency graph
- Detects frameworks (Django, React, Spring, Express...)
- Identifies "hub files" — the most imported, most central modules
- Parses dependencies from `package.json`, `go.mod`, `requirements.txt`

**Phase 2 — LLM Semantic Analysis:**
- Generates a concise project summary
- Creates per-file semantic descriptions
- Analyzes architectural patterns and conventions

The result? When you ask your first question, the agent already knows your project's architecture, key entry points, framework choices, and coding conventions. No wasted turns on exploration.

---

## LSP as a First-Class Tool

Claude Code has limited LSP support. OpenCode and Codex have none. They all fall back to grep — and grep doesn't understand code.

vix runs a full LSP client pool managing language servers with these operations available to the agent:

- **go_to_definition** — jump to where a symbol is defined
- **find_references** — find every usage across the project
- **go_to_type_definition** — navigate to type declarations
- **go_to_implementation** — find interface implementations
- **document_symbols** / **workspace_symbols** — browse the symbol index

Combined with the Brain's pre-computed symbol index, this enables accurate refactoring. Find *all* references, not "grep and pray." The agent navigates code like an IDE, not like a text search.

---

## Tool Orchestrator: Stop Making the LLM Loop

Every other coding agent calls tools one at a time. Read a file? One LLM round-trip. Read another? Another round-trip. Want to search 50 Go files for TODO comments? That's either 50 turns or a creative bash command.

vix's **Tool Orchestrator** lets the LLM write a Python script that executes multiple tools in one atomic step:

```python
files = glob_files("**/*.go")
for f in files:
    content = read_file(f)
    if "TODO" in content:
        print(f"Found TODO in {f}")
```

The orchestrator spawns a Python subprocess with tool wrappers (`read_file`, `grep`, `bash`, `edit_file`, `write_file`, `glob_files`, `lsp_query`) that communicate back to the daemon via JSON IPC. One LLM turn instead of fifty.

This is particularly powerful for:
- Multi-file refactoring with conditional logic
- Codebase-wide analysis and reporting
- Repetitive edits that follow a pattern
- Any task where the LLM would otherwise loop mechanically

---

## Structured Workflows, Not "Chat and Hope"

Claude Code has a basic plan mode. OpenCode and Codex have nothing — you type, you hope.

vix has a **workflow engine** with JSON-defined pipelines:

```
Explore → Plan → Critic → Refine → Review → Execute
```

Each step in a workflow can:
- Run a **sub-agent** with a custom prompt, model, and tool restrictions
- **Chain results** — each step accesses prior outputs via `$(step.id)` syntax
- **Block dangerous tools** in specific phases (no file writes during planning)
- **Control streaming** — show or hide output per phase
- **Parse structured output** — extract JSON from agent responses for the next step

The Critic phase is key. Before any code gets written, a separate agent reviews the plan for feasibility, completeness, and risk. This catches bad plans before they waste execution time.

Workflows are configurable via `.vix/settings.json`. You can define your own pipelines or use the built-in ones.

---

## Sub-Agents with Model Routing

Claude Code's sub-agents all use the same model. vix lets you route different tasks to different models:

- **Opus** for planning and complex reasoning
- **Sonnet** for execution and code generation
- **Haiku** for summaries and memory generation

Each sub-agent gets:
- Independent conversation history
- Per-agent tool filtering (restrict sub-agents to read-only tools)
- Background async execution with task polling
- Max turn limits
- Custom system prompts with template resolution

Use the right model for the right job. Why spend $15/MTok on Opus to read 10 files when Haiku can do it for $0.25?

---

## Memory That Builds Itself

Claude Code has memory, but you have to ask it to remember things. vix's memory is **automatic**.

After each turn, an LLM evaluates whether insights are worth saving. Memories are stored as YAML-frontmatter markdown files in `.vix/memory/`, pruned at 50 files via FIFO, and injected into every system prompt.

The agent gets smarter over time without any user effort. Your project conventions, common patterns, and past decisions carry forward across sessions.

---

## The Small Things That Add Up

### Smart Read Deduplication

Every file read is tracked with line ranges. If the LLM asks to read the same range twice, vix returns an error instead of wasting tokens. The tracker auto-invalidates when files are modified. This saves significant tokens on multi-turn conversations.

### Frequently Accessed Files in System Prompt

A SQLite database tracks file access patterns across sessions. The top 10 most-accessed files are automatically included in every system prompt. Your agent already knows about `main.go` before you mention it.

### Parallel Tool Execution

When the LLM requests multiple read operations, vix runs them concurrently via goroutines. Write tools execute sequentially for safety. Combined with read deduplication, exploration-heavy tasks complete dramatically faster.

### Tree-Sitter Code Compression

Instead of feeding raw file contents into the context window, vix uses Tree-sitter to parse and compress code — keeping function signatures, types, and structure while stripping implementation details. Supports Go, Python, JavaScript/TypeScript, Rust, C/C++, and Java. Fit more code context into the same token budget.

### OS-Level Sandboxing

On macOS, vix generates Seatbelt profiles (`.sb` files) for bash commands — filesystem allow/deny rules, subpath restrictions, and per-command isolation. This is OS-level defense, not "ask the user and hope they read the prompt."

### Prompt Caching Throughout

System prompt blocks, frequently accessed files, and tool definitions all use Anthropic's prompt caching. Cache reads cost $0.30/MTok vs $3/MTok for regular input — a 10x cost reduction that compounds over long sessions.

### Headless Mode for CI/CD

Full headless execution via `-p` flag with text, JSON, or streaming JSON output. Structured results include session ID, token usage, duration, and error state. Script vix into your pipelines.

### Built-in Quality Evaluation

A Python-based LLM-as-Judge system evaluates agent output across 9 dimensions: completeness, feasibility, ordering, actionability, testability, atomicity, risk surface, decision transparency, and scope creep. Automated regression detection for agent behavior.

### Skill Registry

YAML-frontmatter markdown files that encode domain expertise as reusable prompts. Per-skill tool whitelisting, model overrides, template variables, and dynamic context via command substitution. Project-level and user-level skills that compose and share.

### Model-Aware Cost Tracking

Per-turn cost calculation with model-specific pricing (Opus/Sonnet/Haiku rates), including cache write and read tokens at reduced rates. Displayed in the status bar so you always know what you're spending.

---

## The Comparison

| Feature | vix | Claude Code | OpenCode | Codex |
|---|---|---|---|---|
| Daemon-client architecture | Yes | No | No | No |
| Brain (semantic project analysis) | Yes | No | No | No |
| LSP as first-class tool | Yes | Limited | No | No |
| Tool orchestrator (Python DSL) | Yes | No | No | No |
| Structured workflow engine | Yes | Basic plan | No | No |
| Sub-agents with model routing | Yes | Same model | No | No |
| Auto-memory | Yes | Manual | No | No |
| Read deduplication | Yes | No | No | No |
| Frequently accessed files | Yes | No | No | No |
| Parallel tool execution | Yes | Yes | No | No |
| Tree-sitter compression | Yes | No | No | No |
| OS-level sandbox | Yes | No | No | No |
| Quality evaluation framework | Yes | No | No | No |

---

## What It's Not

vix is not trying to replace Claude Code for everyone. Claude Code has a larger team, faster iteration, and features vix doesn't have yet — session persistence, a permission system, auto-compaction, and a richer set of slash commands.

What vix is: a proof that coding agents can be architecturally better. That pre-understanding your project beats on-demand file reading. That structured workflows beat chat-and-hope. That the right model for the right job beats one-model-fits-all.

If you care about agent reliability, cost efficiency, and code intelligence — vix is worth a look.
