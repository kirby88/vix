<p align="center">
  <img src="assets/logo_animated_pulse.svg" alt="vix" width="200" />
  <br/>
  <p align="center">Other coding agents waste tokens. Vix doesn't.</p>
  <br/>
  <img src="assets/vix_presentation.gif" alt="vix" width="700" />
</p>

[![GitHub Release](https://img.shields.io/github/v/release/kirby88/vix?color=green)](https://github.com/kirby88/vix/releases)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202-blue)](https://github.com/kirby88/vix/LICENCE)

## Features

### Workflows

Most coding agents have immutable workflows. For example claude code plan mode follow this pattern: exploration, planning, execution, validation.

Vix takes a different approach: it lets you define **workflows**, sequences of discrete steps the agent follows. You control what context is shared between steps and what gets discarded. 

For example `vix` native plan mode enables plan and  . This frees up "mental space" for the LLM to focus on one thing at a time, which has been proven to be significantly more effective. 

**The result is more determinism** in how the agent behaves: instead of a single open-ended prompt doing everything at once, each step does one thing well. You can experiment with different workflows, tweak them, and iterate — your agent, your rules.