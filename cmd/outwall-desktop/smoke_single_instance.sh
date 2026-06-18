#!/usr/bin/env bash
# Single-instance smoke test (ADR-0013). Launches two outwall-desktop instances under a
# headless X server with a THROWAWAY $HOME (never touches the real vault/instance) and asserts:
#   * instance #1 comes up and its outwall.sock answers;
#   * instance #2 exits 0 (focus handed off to #1) with NO "address already in use";
#   * instance #1 is still running after #2 exits.
# Skips (exit 0) with a clear message if no headless X / dbus tooling is available.
set -u

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
bin="$repo/dist/bin/outwall-desktop"

skip() { echo "SKIP: $*"; exit 0; }
fail() { echo "FAIL: $*"; exit 1; }

command -v xvfb-run >/dev/null 2>&1 || skip "xvfb-run not available (no headless X)"
command -v dbus-run-session >/dev/null 2>&1 || skip "dbus-run-session not available"
[ -x "$bin" ] || fail "binary not built: $bin (run: make build-desktop)"

work="$(mktemp -d)"
export HOME="$work"        # throwaway data dir → $HOME/.spk/outwall
sock="$HOME/.spk/outwall/outwall.sock"
log1="$work/inst1.log"
log2="$work/inst2.log"

cleanup() {
  [ -n "${pid1:-}" ] && kill "$pid1" 2>/dev/null
  # xvfb-run leaves an Xvfb child; kill the whole process group of pid1 best-effort.
  [ -n "${pid1:-}" ] && pkill -P "$pid1" 2>/dev/null
  rm -rf "$work"
}
trap cleanup EXIT

echo "== launching instance #1 (xvfb, HOME=$HOME)"
xvfb-run -a dbus-run-session -- "$bin" >"$log1" 2>&1 &
pid1=$!

# Poll for the admin socket to appear (instance #1 daemon is up).
for _ in $(seq 1 100); do
  [ -S "$sock" ] && break
  kill -0 "$pid1" 2>/dev/null || fail "instance #1 died during startup:
$(cat "$log1")"
  sleep 0.2
done
[ -S "$sock" ] || fail "instance #1 socket never appeared:
$(cat "$log1")"
echo "   instance #1 up (pid $pid1), socket ready"

echo "== launching instance #2 (must hand focus off and exit 0)"
xvfb-run -a dbus-run-session -- "$bin" >"$log2" 2>&1 &
pid2=$!

# Wait (bounded) for instance #2 to exit.
rc2=""
for _ in $(seq 1 50); do
  if ! kill -0 "$pid2" 2>/dev/null; then
    wait "$pid2"; rc2=$?
    break
  fi
  sleep 0.2
done
[ -n "$rc2" ] || { kill "$pid2" 2>/dev/null; fail "instance #2 did not exit within ~10s:
$(cat "$log2")"; }

if grep -qi "address already in use" "$log2"; then
  fail "instance #2 hit a port-bind conflict (gate did NOT run before the daemon bound):
$(cat "$log2")"
fi
[ "$rc2" -eq 0 ] || fail "instance #2 exit code $rc2 (expected 0):
$(cat "$log2")"
echo "   instance #2 exited 0, no port-bind error"

kill -0 "$pid1" 2>/dev/null || fail "instance #1 did not survive instance #2's launch:
$(cat "$log1")"
echo "   instance #1 still running"

echo "PASS: single-instance + focus hand-off works"
