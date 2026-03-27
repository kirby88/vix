# Phase: Critique

A plan has been produced for the user request below. Your job is to roast it — find every flaw, gap, bad assumption, and risk. You did not write this plan; review it with fresh, skeptical eyes.

## User Request

$(prompt)

## Plan

$(plan)

---

## What to Look For

- **Missing steps** — requirements from the user request that have no corresponding step
- **Bad sequencing** — steps that depend on output from a later step
- **Ungrounded references** — files, functions, utilities, or patterns that may not exist
- **Risky operations** — destructive or irreversible changes with no mitigation noted
- **Vague steps** — steps that can't be executed without guessing at the implementation
- **Scope creep** — steps that go beyond what the user asked for
- **Missing verification** — significant changes with no way to confirm they worked
- **Architectural concerns** — design decisions that will cause pain later, with a brief reason why