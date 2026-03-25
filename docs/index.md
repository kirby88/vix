---
layout: default
title: vix — the coding agent that doesn't waste tokens
description: Other coding agents waste tokens. Vix doesn't.
---

<p align="center">
  <img src="https://raw.githubusercontent.com/kirby88/vix/main/assets/logo_animated_pulse.svg" alt="vix" width="160" />
</p>

<p align="center">
  <strong>Other coding agents waste tokens. Vix doesn't.</strong>
</p>

<p align="center">
  <img src="https://raw.githubusercontent.com/kirby88/vix/main/assets/vix_presentation.gif" alt="vix demo" width="700" />
</p>

<p align="center">
  <a href="https://github.com/kirby88/vix/releases"><img src="https://img.shields.io/github/v/release/kirby88/vix?color=green" alt="GitHub Release" /></a>
  <a href="https://github.com/kirby88/vix/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-Apache%202-blue" alt="License: Apache 2.0" /></a>
</p>

---

## Features

### Workflows

Most coding agents have immutable workflows. For example, Claude Code's plan mode follows a fixed pattern: exploration → planning → execution → validation.

Vix takes a different approach: it lets you define **workflows** — sequences of discrete steps the agent follows. You control what context is shared between steps and what gets discarded.

For example, `vix` native plan mode separates planning from execution. This frees up "mental space" for the LLM to focus on one thing at a time, which has been proven to be significantly more effective.

**The result is more determinism** in how the agent behaves: instead of a single open-ended prompt doing everything at once, each step does one thing well. You can experiment with different workflows, tweak them, and iterate — your agent, your rules.

---

## Get Started

```bash
# Install the latest release
go install github.com/kirby88/vix/cmd/vix@latest
go install github.com/kirby88/vix/cmd/vix-daemon@latest

# Set your API key
export ANTHROPIC_API_KEY=your_key_here

# Start the daemon and client in separate terminals
vix-daemon
vix
```

---

## Links

- [GitHub Repository](https://github.com/kirby88/vix)
- [Releases](https://github.com/kirby88/vix/releases)
- [Contributing](https://github.com/kirby88/vix/blob/main/CONTRIBUTING.md)
- [License (Apache 2.0)](https://github.com/kirby88/vix/blob/main/LICENSE)
