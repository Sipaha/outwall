import { relTime, absTime } from '../lib/relativeTime'

/** RelTime shows a compact relative time ("4m ago") with the exact time in the hover title.
 *  An empty/absent timestamp renders the `empty` fallback (default "Never"). */
export function RelTime({ iso, empty = 'Never' }: { iso: string; empty?: string }) {
  if (!iso) return <>{empty}</>
  return <span title={absTime(iso)}>{relTime(iso)}</span>
}
