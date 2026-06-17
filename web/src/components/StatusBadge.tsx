// Status → theme-aware CSS color token (defined in index.css --color-status-*). A var keeps
// the pill legible on the dark surface and centralizes the palette.
const C_RUNNING = 'var(--color-status-running)' // active / allow / granted
const C_TRANSIENT = 'var(--color-status-transient)' // new / pending
const C_STALLED = 'var(--color-status-stalled)' // require-approval / denied-attention
const C_STOPPED = 'var(--color-status-stopped)' // inactive / dismissed

// Maps the daemon's status/outcome strings (agent.status, rule.outcome, access-request.status,
// approval state) onto a color. Unknown values fall back to the muted "stopped" gray.
const statusColor: Record<string, string> = {
  // agent status
  new: C_TRANSIENT,
  active: C_RUNNING,
  // approval / access-request / generic
  pending: C_TRANSIENT,
  granted: C_RUNNING,
  denied: C_STALLED,
  dismissed: C_STOPPED,
  // rule outcomes
  allow: C_RUNNING,
  deny: C_STALLED,
  'require-approval': C_STALLED,
}

export function StatusBadge({ status }: { status: string }) {
  const color = statusColor[status] ?? C_STOPPED
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded px-1.5 py-0 text-[11px] font-medium leading-[17px]"
      style={{ backgroundColor: `color-mix(in srgb, ${color} 14%, transparent)`, color }}
    >
      <span className="inline-block h-1.5 w-1.5 rounded-full" style={{ backgroundColor: color }} />
      {status}
    </span>
  )
}
