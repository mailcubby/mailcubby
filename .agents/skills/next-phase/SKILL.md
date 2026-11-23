---
name: next-phase
description: Implements the next unfinished phase of docs/PLAN.md for the Mailcubby repository, following AGENTS.md. Use when asked to continue the project, start the next phase, or work through the implementation plan.
---

# Next phase

`AGENTS.md` (repository root) is the single source of truth and is already loaded as a rule. This skill only sequences it; if anything here seems to conflict with `AGENTS.md`, `AGENTS.md` wins.

1. Read `docs/SPEC.md` and `docs/ARCHITECTURE.md` in full, `docs/UI.md` if the phase has UI tasks, and the ADRs in `docs/decisions/` that touch the phase.
2. Open `docs/PLAN.md` and select the first phase (including `a`/`b` parts) with unchecked tasks. Work on that phase only.
3. Produce an implementation plan artifact: files to create or change, tests (Go, Vitest, Playwright) mapped to each acceptance criterion, and spec ambiguities. Continue without waiting for approval unless a stop condition from `AGENTS.md` section 7 applies or the owner asked to review plans.
4. Implement task by task. After each task run the relevant tests and tick its checkbox in `docs/PLAN.md`.
5. Before finishing, run `make check`; for phases with UI tasks also `make e2e`; for concurrency changes also `make test-race`. Run long-lived servers in the background and stop them afterwards.
6. Optionally use the browser to open the running app (`make run` in the background, `http://localhost:8025`) and attach screenshots of changed screens. Playwright specs remain the acceptance evidence.
7. Finish with the summary required by `AGENTS.md` section 9: what changed, how it was tested, blocked or owner-verified criteria, and new entries under "Open questions" in `docs/PLAN.md`.
