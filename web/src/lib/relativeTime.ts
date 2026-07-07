// relTime renders an RFC3339 timestamp as a compact relative string ("just now", "4m ago",
// "2h ago", "3d ago"); anything older than a week falls back to an absolute short date. Returns the
// raw string unchanged if it doesn't parse. Pair it with absTime() in a title attribute so hovering
// reveals the exact time.
export function relTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  const sec = Math.round((Date.now() - d.getTime()) / 1000)
  if (sec < 45) return 'just now' // also covers small future clock skew (sec < 0)
  const min = Math.round(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr}h ago`
  const day = Math.round(hr / 24)
  if (day < 7) return `${day}d ago`
  return d.toLocaleDateString()
}

// absTime renders the full localized date+time — for the hover title behind a relTime() label.
export function absTime(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}
