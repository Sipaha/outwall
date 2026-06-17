# Supervisor mode — autonomous phase development

> Read by the **top-level supervisor only** (the Claude Code main session in autonomous
> mode). Sub-agents must NOT read this — they get a narrow task scope from the dispatch prompt.
>
> Trigger: the user says "implement autonomously", "develop further", "work on … automatically",
> or points a direction without explicit steps.

## TL;DR

```
1. READ      → INDEX.md + current-phase.md + git log -10 ; ensure clean main
2. PLAN      → scope the phase; ADR-worthy decisions → a new ADR; the full spec lives in
               the dispatch prompt or the docs/superpowers/ plan (NO phase-N spec docs)
3. DISPATCH  → Agent(subagent_type=general-purpose) with a detailed prompt (8 sections)
4. VERIFY    → independently re-run make fmt/vet/test; read the sub-agent's new tests
5. FINALIZE  → module docs / ADR status / current-phase.md (forward-only); commit
6. NEXT      → pick Phase N+1 (or ask if a major direction shift)
```

Cadence: one phase ≈ one coherent, independently-testable milestone. Every 3–4 phases — a
short milestone summary to the user.

> **The supervisor's job includes optimizing its own + the sub-agents' workflow.** Treat
> friction as a fixable bug. A recurring manual step → script it. An instruction that tripped
> a sub-agent → tighten the prompt template. The playbook itself (this file, the prompt
> template, doc-discipline) is in scope to improve — commit those changes like any other.

## 1. READ

```bash
git -C <project> log --oneline -10
git -C <project> status --porcelain     # MUST be empty before dispatch
sed -n '1,40p' docs/roadmap/current-phase.md
```

If `main` is dirty — commit or stash before dispatching. A clean base makes "sub-agent's
worktree base = main at dispatch time" provably true.

## 2. PLAN — scope the phase (no phase-N spec docs)

1. **Scope it.** Pick the candidate from `current-phase.md` (or the user's direction). The
   full specification — context, acceptance criteria, the exact API, files, tests — lives in
   the **dispatch prompt** or the existing `docs/superpowers/plans/` plan. The prompt is
   ephemeral; anything worth keeping lands in an ADR / module doc / finding at finalize.
2. **ADR-worthy decisions → an ADR.** New subsystem, data-format change, deliberate
   deviation → `docs/architecture/decisions/NNNN-<slug>.md` (next free number) per
   `adr-template.md`. Either you write it pre-dispatch (status `proposed`, sub-agent
   implements against it) or the sub-agent writes it (status `accepted`).
3. **Update `current-phase.md`** — set the active section to the new work + its goal
   (forward-looking only).

## 3. DISPATCH — sub-agent prompt

```
Agent({
  description: "Phase N: <short>",
  subagent_type: "general-purpose",
  prompt: `<full template below>`,
  run_in_background: true,
  isolation: "worktree"   // MANDATORY when dispatching ≥2 write-agents in one message
})
```

### Mandatory prompt sections (≥8)

```
## MANDATORY BEFORE WORK
1. AGENTS.md — the hard rules.
2. The relevant ADRs + the plan/spec path (give exact paths).
3. The existing files you will edit (give exact paths).
4. docs/workflow/adr-template.md (if creating an ADR).

## SCOPE — sections A, B, C…
For each: the specific API (struct/func signatures), the files created/edited, the tests to write.

## HARD RULES
> These "NO X" rules forbid X in your CODE — they do NOT forbid running tooling.
> `go build` / `go test` / `go vet` / `gofmt` / `make …` write only to build output and
> are REQUIRED, not "modifications". If a rule's scope feels unclear, ask — don't guess
> restrictively and work blind.
- Module path is github.com/Sipaha/outwall. NO `citeck` anywhere.
- NO CGO in the server binary (CGO_ENABLED=0); SQLite via modernc.org/sqlite.
- NO panics / log.Fatal in library code — return wrapped errors. Panics only in main/tests.
- NO Co-Authored-By in commits. NO git commit --amend / no rewriting commits. DO commit normally.
- NO #[t.Skip] on a flaky test to silence it — a flaky test is a real shared-resource bug
  (hardcoded port, shared temp path, global state). Fix the root cause (t.TempDir(),
  OS-assigned :0 ports, per-test state) OR surface it in the REPORT with the test name +
  symptom — never silence it.
- Tests must make a real assertion on behaviour, not on their own setup. Don't pad the count
  with near-duplicates. Exercise the real code path, not a hand-rolled stand-in.
- DON'T touch docs/INDEX.md / docs/roadmap/current-phase.md (the supervisor finalizes those)
  — but DO update module docs / findings / the ADR as the DOCUMENTATION section lists.

## CHECKS
cd <project>
make fmt
make vet
go test ./... > /tmp/outwall_test_phaseN.txt 2>&1   # ONE run; analyse the FILE, don't re-run
grep -E "FAIL|panic|^ok|^---" /tmp/outwall_test_phaseN.txt
make build
All green before you commit.

## ADR
Create docs/architecture/decisions/NNNN-<slug>.md per the template. Status: accepted, date: <YYYY-MM-DD>.

## DOCUMENTATION
- DON'T update docs/INDEX.md and docs/roadmap/current-phase.md (supervisor does it).
- DON'T create phase-N spec/changelog docs or commit screenshots.
- Module docs (docs/architecture/modules/<pkg>.md) — update if the public API changed.
- Findings — docs/findings/YYYY-MM-<slug>.md if you discovered something non-obvious.

## COMMIT
git add -A
git commit -m "Phase N: <one-line summary>

- <bullet 1>
- <bullet 2>

Tests: <num> passing."
NO Co-Authored-By. NO amends.

## REPORT (≤400 words)
- Files created/changed; the commit SHA(s).
- make fmt/vet/test/build results (paste the test summary line).
- Acceptance items verified end-to-end.
- What's left / gotchas / deferred.
- Anything important not in the docs (architecture bottlenecks, fragile abstractions,
  surprising couplings) — surface it even if out of scope; the supervisor decides if it
  becomes a follow-up / ADR / finding. Don't sit on it.
- If you WORKED AROUND something instead of fixing it properly — say so explicitly, with
  what the proper fix would be and what it'd touch. (Quality bar: hacks aren't acceptable;
  a clean fix needing an out-of-scope architecture change is an escalation, not a quiet ship.)
- If you saw flaky tests — name them (file + func) and the symptom. "Some tests were flaky"
  with no names is unactionable.
```

### When to parallelize

If 2+ directions are **independent** (different packages / non-overlapping files) — dispatch
in one message with several `Agent` calls. **Every parallel write-phase dispatch MUST use
`isolation: "worktree"`** — no exceptions. Parallel agents on the same working tree trample
each other's `go build`/file writes. The supervisor merges each `worktree-agent-<id>` branch
into `main` sequentially after completion, resolving conflicts (most common: `go.mod`/`go.sum`
— regen via `go mod tidy`; ADR/module-doc section ordering — keep both addenda).

Most of outwall's Plan 1 (foundation) is a **dependency chain** (store → vault → registries →
proxy → daemon) — dispatch it as a single sequential agent, not parallel. Later milestones
(backend vs web UI) are more parallelizable.

#### Pre-dispatch invariant (always, for worktree isolation)

```bash
git -C <project> rev-parse --abbrev-ref HEAD     # MUST print "main"
git -C <project> status --porcelain | head -1     # MUST be empty
```

If either fails, resolve before dispatching (`git checkout main`; commit or stash dirty WD).

## 4. VERIFY — after the sub-agent notifies

```bash
git -C <project> log --oneline -3      # did it actually commit?
git -C <project> status                # clean? (if not, it forgot to commit)
make fmt    # or: gofmt -l . (must print nothing)
make vet
go test ./... > /tmp/outwall_verify.txt 2>&1 ; grep -E "FAIL|^ok|panic" /tmp/outwall_verify.txt
make build
```

**Don't trust sub-agent claims — re-run the gates yourself**, and **spot-read its new tests**:
real assertions on behaviour, or tautologies/stubs (`assert.True(t, true)`, asserting the
thing it just set, N near-duplicates padding the count, a test that builds a stand-in the bug
doesn't touch)? If the sub-agent claims "feature works", confirm via a test or a real run
(e.g. the CLI/curl smoke test in the plan), not its word.

If it forgot to commit: `git add -A && git commit -m "<message from its report>"`.

## 5. FINALIZE

- The phase's ADR → status `accepted`; fold in any deviations the sub-agent reported.
- `docs/roadmap/current-phase.md` → drop finished work; switch to Phase N+1 (forward-only,
  no "recently completed" section).
- `docs/INDEX.md` → only if the phase added a doc — link it.
- `README.md` → only if the user-visible surface changed.
- Module docs → verify the sub-agent updated `docs/architecture/modules/<pkg>.md` for any
  changed public API (do it yourself if it forgot).
- Commit:
  ```bash
  git add -A
  git commit -m "docs: finalize <phase summary>

  - ADR-NNNN → accepted (+ <deviation note>)
  - current-phase.md: active → <next>"
  ```
- **Push:** only `main`, over `git@github.com-sipaha:Sipaha/outwall.git`. **Confirm with the
  user before the first push of a session** unless they've durably authorized pushing; don't
  force-push; if a push fails, report it.

## 6. NEXT

- **Self-pick** when candidates are queued in `current-phase.md`, the user didn't point a
  direction, and nothing critical waits.
- **Ask** on a major direction shift, a needed design decision, or when user feedback
  changes priority.

When the user gives several concepts ("do X, Y, Z") — bundle related ones into one phase,
otherwise queue them sequentially.

## Anti-patterns (look here after every phase)

- ❌ **Trusting sub-agent claims without verifying.** "Tests pass" → run `go test` anyway.
  "Feature works" → a test or a real run. "Uses the X API" → `grep` the actual path.
- ❌ **Counting tests instead of reading them.** Spot-check for tautologies, duplicates,
  stand-ins that miss the real path.
- ❌ **Letting a workaround slip into `main`.** A quiet shortcut around a broken abstraction
  → don't finalize. Re-dispatch with the right scope, or escalate the architecture trade-off
  to the user. A reasoned ADR'd deviation is fine; a hack is not.
- ❌ **Categorical instructions an over-literal agent mis-reads.** A bare "NO X" / "read-only"
  gets read too broadly (a "don't modify files" agent that refused to run `go test`). Say what
  the prohibition does NOT preclude.
- ❌ **Skipping the finalize commit** — the next phase then starts from stale docs.
- ❌ **Dispatching without a detailed (≥8-section) prompt** — the sub-agent hallucinates.
- ❌ **Parallel write-agents without `isolation: "worktree"`** — they trample the working tree.
- ❌ **Re-running a long command to "see the output differently"** — capture to a file once,
  then grep/awk the file.
- ❌ **"Looks flaky, moving on" without names + symptoms** — almost always a shared-resource
  race; fixable at root. Name the test or it's unactionable.

## Checklist — before each dispatch

- [ ] Read INDEX.md + current-phase.md.
- [ ] `git status` clean; on `main`.
- [ ] The dispatch prompt carries the FULL spec (≥8 sections) or points at the plan file.
- [ ] ADR-worthy decisions have an ADR (written or assigned to the sub-agent).
- [ ] If parallel — every agent is `isolation: "worktree"`.

After the sub-agent finishes:
- [ ] `git status` clean (it committed).
- [ ] make fmt/vet/test/build all green — re-run yourself.
- [ ] Spot-read the new tests.
- [ ] FINALIZE done: ADR status, current-phase.md advanced, module docs verified.
- [ ] Finalize commit made (+ pushed, with user OK).
