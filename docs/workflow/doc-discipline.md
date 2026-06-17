# Doc discipline — what to write where

> Docs answer **"what is" / "what will be"**. The record of **how things changed** is the
> **ADR + git history** — not the docs. No per-phase changelog files, no `history.md`, no
> committed screenshots, no `phase-N-<slug>.md` spec docs (the full spec lives in the
> dispatch prompt or the `docs/superpowers/` plan; anything worth keeping lands in an ADR /
> module doc / finding).

## The doc types

| Dir | Holds | Format |
|---|---|---|
| `docs/INDEX.md` | the map of all docs | link list |
| `docs/roadmap/current-phase.md` | the active phase + next candidates (forward-only) | prose |
| `docs/architecture/overview.md` | the overall system picture | prose |
| `docs/architecture/decisions/NNNN-<slug>.md` | one architectural decision each | ADR (see `adr-template.md`) |
| `docs/architecture/modules/<pkg>.md` | a package's public API + responsibility | prose |
| `docs/findings/YYYY-MM-<slug>.md` | a non-obvious library/pattern/gotcha discovered | prose |
| `docs/guides/<slug>.md` | a recurring pattern (≥2×) worth a how-to | prose |
| `docs/superpowers/specs/` & `plans/` | brainstormed design specs + implementation plans | per the superpowers skills |

## Rules

- **The ADR is the decision record.** Every architectural decision (new subsystem,
  data-format change, a deliberate deviation) is one ADR file. Don't narrate decisions in
  module docs — link the ADR.
- **git is the changelog.** Don't keep "recently completed" sections, phase tables, or
  changelog files in `docs/`. `git log` already has it.
- **Module docs describe the current API**, not its history. When the API changes, edit the
  module doc in the same commit; don't append a "changed in phase N" note.
- **`current-phase.md` is forward-only.** It says what's active and what's next. When a phase
  finishes, drop it and advance — the finished work is visible in git.
- **New doc → link it from `INDEX.md`** in the same commit.
- **No committed screenshots / binaries.** Put transient artefacts in `/tmp`.
