# Agent Loop — the routine for each task

> Run on each task/phase. The goal: don't drift architecturally, don't lose knowledge,
> don't leave debt in the documentation.

## The six phases

### 1. READ — gather context

- `AGENTS.md`/`CLAUDE.md` (the hard rules; usually already in context).
- `docs/INDEX.md`, `docs/roadmap/current-phase.md`.
- The ADRs relevant to the task (`docs/architecture/decisions/`).
- The module docs of the packages you'll edit (`docs/architecture/modules/`).
- The relevant spec/plan under `docs/superpowers/`.

**Signal you've read enough:** you can answer "what am I doing and why exactly this way" in
one or two sentences.

### 2. DECIDE — fix the decisions

Before code: which ADRs cover this change? If none — is a new ADR needed? Which module doc
changes? Which tests verify the task? If a new ADR is needed, **write it first** (status
`proposed` is fine), then the code.

### 3. CODE — implement

- Small commits, TDD: failing test → minimal code → green → commit.
- No panics/`log.Fatal` in library code — return wrapped `error`s.
- Don't break the hard rules in `AGENTS.md`.

### 4. TEST — verify

```bash
make fmt
make vet
make test
```

All green before moving on. A failing test is never "unrelated" — fix it or stop the task.

### 5. DOC — update the documentation (same commit as the code)

| What changed | What to update |
|---|---|
| A package's public API | `docs/architecture/modules/<pkg>.md` |
| An architectural decision | a new ADR or the status of an existing one |
| Found a useful library / pattern | a new `docs/findings/YYYY-MM-<slug>.md` |
| Progress on the phase | `docs/roadmap/current-phase.md` |
| A new recurring pattern (≥2×) | a new guide in `docs/guides/` |
| A new file in `docs/` | `docs/INDEX.md` |

### 6. COMMIT

```
<phase>: <what you did>

- what changed in the code (1-3 items)
- what changed in the docs (1-2 items)

ADR: 0001 (if applicable)
```

No `Co-Authored-By`. No `--amend`.

## Anti-patterns

- ❌ Jumping into code without READ — you'll work off a stale picture and rewrite.
- ❌ Skipping an ADR "because it's small" — small drifts compound.
- ❌ Deferring the doc-update to "later" — later never comes; it's part of acceptance.
- ❌ Ignoring a failing test "it's not related" — it is. Fix now or stop.
- ❌ Doing a dependency upgrade as part of a feature — upgrades are separate tasks.

## If the task grew

1. Stop. 2. Commit current progress as `WIP: …`. 3. Update `current-phase.md` with the
expansion. 4. New ADR (`proposed`) if it's an architectural decision. 5. Ask the user
whether to continue or narrow scope.

## If you get stuck

Don't make things up. Ask.
