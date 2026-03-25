---
## 📐 Entering Phase: Plan
You are now in the **Plan** phase. Set aside any exploration findings or assumptions from previous phases — they no longer apply. Your only objective is defined below.
---

# Phase: Plan

Produce a structured implementation plan for the user request below. Do not write or modify any code.

## User Request

$(prompt)

---

## Plan Format

### Name
Short, specific label. 2–5 words. Not a sentence.

### Context
**Why** this change is needed. What problem does it solve? What degrades or breaks without it? Do not describe what the code will do — explain the motivation.

### Architecture
Structural or design-level changes only. Omit if this is a purely self-contained implementation. Cover: new abstractions introduced, interfaces changed, data flow affected, new dependencies added. For each architectural decision, briefly state **why** that approach was chosen.

### Files
Exhaustive list of every file that will be **created** or **modified**. No directories. No read-only files. Verify uncertain paths with a tool before listing them.

### Steps
Ordered implementation steps. Each step must:

- Name **specific identifiers**: file path, function/method, type, interface
- Call out **existing utilities to reuse** rather than reimplementing
- **Flag risky steps** inline (e.g. *"⚠ modifies shared interface — all implementors must be updated in subsequent steps"*)
- Include **inline verification** after significant steps where applicable
- End with a **final Verify step** — exact build and test commands confirming the full change is correct

**Step quality bar:**
- Specific enough to execute without ambiguity — not so detailed it dictates variable names or formatting
- One coherent unit of work per step — no bundling of unrelated changes
- Ordered so no step depends on the output of a later step
- No steps beyond what the request asks — note related problems as asides, do not fold them in

**Step anti-patterns to avoid:**
- Vague verbs: *"update"*, *"handle"*, *"improve"* — use *"add"*, *"replace"*, *"extract"*, *"delete"*, *"rename"*
- Referencing code that may not exist
- Adding unrequested refactoring or speculative improvements

---

## Output

Write the plan in full. Then, before finalising it, review it against these questions:

- Does every step reference real, verified identifiers — no invented file paths or function names?
- Is every step ordered such that no step depends on the output of a later step?
- Are there any steps that bundle unrelated changes?
- Are there any vague verbs that should be made more specific?
- Does the Files list match exactly what the steps touch — nothing missing, nothing extra?
- Does the final Verify step include exact commands?

If any answer reveals a problem, silently fix the plan. Then output the final, corrected plan.