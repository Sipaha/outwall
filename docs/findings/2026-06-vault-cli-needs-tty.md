# Finding: `vault init`/`unlock` CLI needs a real TTY

**Date:** 2026-06-17 (Plan 1)

## What

`outwall vault init` and `outwall vault unlock` read the master password via
`golang.org/x/term`'s `term.ReadPassword(0)` (fd 0 = stdin). This requires stdin to be a
**real terminal**. Piping does not work:

```bash
printf 'pw\n' | outwall vault unlock      # FAILS: "inappropriate ioctl for device"
```

To script/test it you need a pty, e.g. `script -qec 'outwall vault unlock' /dev/null <<< pw`.

## Why it matters

- The Wails desktop UI (Plan 7) collects the master password through a UI field and calls the
  admin API `POST /vault/unlock` directly — it does **not** go through the CLI prompt, so the
  desktop path is unaffected.
- But headless/automated use (CI, smoke tests, a future server mode) needs a non-TTY way in.

## Recommended follow-up (later plan)

Add a `--password-stdin` flag to `vault init`/`unlock` that reads the password from stdin
(trimming a trailing newline) instead of prompting, mirroring the common `docker login
--password-stdin` pattern. Keep the interactive `term.ReadPassword` prompt as the default.
Not a bug in Plan 1 — a known ergonomics gap, deferred deliberately.
