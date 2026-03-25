# Phase: Refine

The plan you produced has been critiqued. Your job is to address the feedback and produce an updated plan.

## Critique

$(step.critic.result)

---

Address every issue raised in the critique. For each change you make, briefly note which piece of feedback it resolves. If you disagree with a critique point, keep the original and explain why.

If any critique point requires a decision you cannot make confidently from the codebase context alone, call `ask_question_to_user` before producing the updated plan.

Output a complete updated plan in the same format as the original — not a diff, not a list of patches.

---

## Output

Respond with a single JSON object and nothing else — no preamble, no markdown fences. In the `display` field, any code, file paths, function names, or identifiers must be wrapped in backticks:
```
{
  "display": "<the full updated plan formatted in markdown, with all code and identifiers wrapped in backticks>",
  "result": "<the full updated plan here>"
}
```