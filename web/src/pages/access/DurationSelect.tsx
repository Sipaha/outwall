/* eslint-disable react-refresh/only-export-components --
 * Shared duration constants (DURATION_OPTIONS, DEFAULT_TTL_SECONDS) are consumed by RuleRow,
 * ApprovalCards, and ManualRuleModal alongside the DurationSelect component itself. */
export const DURATION_OPTIONS: { label: string; seconds: number }[] = [
  { label: '1 час', seconds: 3600 },
  { label: '2 часа', seconds: 7200 },
  { label: '8 часов', seconds: 28800 },
  { label: '24 часа', seconds: 86400 },
  { label: '2 дня', seconds: 172800 },
  { label: '7 дней', seconds: 604800 },
  { label: '1 месяц', seconds: 2592000 },
  { label: '1 год', seconds: 31536000 },
  { label: 'Бессрочно', seconds: 0 },
]

export const DEFAULT_TTL_SECONDS = 3600

/** DurationSelect is the shared grant-duration dropdown (approval card, manual grant, renew).
 *  value/onChange are ttl_seconds (0 = Бессрочно / never expires). */
export function DurationSelect({
  value,
  onChange,
  className,
}: {
  value: number
  onChange: (seconds: number) => void
  className?: string
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      className={
        className ??
        'rounded-md border border-border bg-card px-2 py-1 text-[12.5px] text-foreground'
      }
      aria-label="grant duration"
    >
      {DURATION_OPTIONS.map((o) => (
        <option key={o.seconds} value={o.seconds}>
          {o.label}
        </option>
      ))}
    </select>
  )
}
