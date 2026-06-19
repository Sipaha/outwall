# ADR-0018: Audit auto-prune + operational hardening (`--password-stdin`, headless)

- **Status:** accepted
- **Date:** 2026-06-19

## Context

Audit (ADR-0004) kept everything by default with only a manual prune (CLI `audit prune` /
Settings "Prune now"). An always-on gateway accrues audit rows unboundedly, so the operator needs a
**retention policy** that the daemon enforces on its own. Two adjacent operational gaps were closed
in the same milestone: unlocking the vault required a real TTY (the finding
`2026-06-vault-cli-needs-tty.md` — blocking CI / automation), and "headless server mode" was
implemented but undocumented.

## Decision

- **Settings store.** A new `settings(key TEXT PRIMARY KEY, value TEXT)` table plus
  `store.GetSetting/SetSetting` (a missing key returns `("", false, nil)`, not an error). One
  general-purpose KV table rather than a column-per-setting, so future operator settings are cheap.
- **Retention + background pruner.** `audit.Recorder` gains `RetentionDays()/SetRetentionDays(int)`
  (persisted under `audit_retention_days`; `0` = keep all), `PruneByRetention(now)`, and
  `RunPruner(ctx, interval)`. `daemon.Serve` launches `RunPruner(ctx, PruneInterval)` in a goroutine
  bound to the serve context (default `DefaultPruneInterval` = 1h; reads the *current* stored value
  each tick, so a change via the API takes effect without a restart; exits on `ctx.Done()`). Admin
  API: `GET /settings/audit-retention` and `PUT /settings/audit-retention {days}` (validated ≥0),
  surfaced in the Settings screen alongside the existing manual prune.
- **`vault --password-stdin`.** `vault init` and `vault unlock` take a `--password-stdin` flag; the
  shared `readPassword(in, fromStdin, prompt)` helper reads all of stdin and trims exactly one
  trailing `\n` (and a preceding `\r`) when the flag is set, else uses the interactive
  `term.ReadPassword` prompt. Resolves the TTY finding.
- **Headless mode.** Confirmed `outwall serve` already runs the full daemon GUI-free; documented it
  (the `daemon` module doc's "Headless / server mode" section) rather than adding a redundant flag.

## Alternatives considered

- **A column per setting (e.g. `vault_meta.audit_retention`).** Rejected: every new operator
  setting would need a schema change; a KV table absorbs them.
- **Prune on each write / a cron-like scheduler.** Rejected: pruning on the hot request path adds
  latency and lock contention; a single low-frequency background ticker is simpler and bounded.
- **A separate `--headless` flag.** Rejected: `serve` is already headless; a flag would be a no-op
  that implies the default is *not* headless. Documentation was the actual gap.

## Consequences

- Operators can cap audit growth with a single retention value; the daemon enforces it hourly with
  no manual step. Manual `Prune`/`audit prune` still works for ad-hoc cleanup.
- Automation/CI can unlock the vault without a pty (`--password-stdin`), and headless operation is a
  documented, supported mode.
- The pruner reads retention each tick, so there is a ≤1h lag between changing the setting and the
  new policy taking effect — acceptable for a retention window measured in days.
- A future "max audit rows" or size-based cap would extend `PruneByRetention` (or add a sibling) and
  reuse the same goroutine.
