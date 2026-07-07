import type { Rule, AccessRequest } from './types'

export interface Grant {
  agentId: string
  upstreamId: string
  rules: Rule[]
  purpose: string // from the most-recent granted access-request for the pair, "" if none
  grantedAt: string // that request's resolved_at, "" if none
}

/** scopeOf derives the coloured scope badge (READ/WRITE/method/verb/browse) for a rule. */
export function scopeOf(rule: Rule): {
  label: string
  kind: 'method' | 'read' | 'write' | 'verb' | 'browse'
} {
  if (rule.profile === 'citeck') {
    const op = (rule.profile_params?.['op'] as string | undefined) ?? 'read'
    return op === 'write' ? { label: 'WRITE', kind: 'write' } : { label: 'READ', kind: 'read' }
  }
  if (rule.browse_path) return { label: 'BROWSE', kind: 'browse' }
  if (rule.namespace || rule.resource || rule.verb) {
    return { label: rule.verb || '*', kind: 'verb' }
  }
  return { label: (rule.op_method || '*').toUpperCase(), kind: 'method' }
}

/** valueSummary renders the non-`*` value constraints of a rule as a compact "var: a, b · n: 1–50"
 *  string (for the collapsed rule tail). Variables set to "any" are omitted. Empty if none constrained. */
export function valueSummary(rule: Rule): string {
  const pols = rule.op_value_policies ?? {}
  const parts: string[] = []
  for (const [name, p] of Object.entries(pols)) {
    if (p.mode === 'any') continue
    if (p.type === 'number' && p.mode === 'range') {
      const lo = p.min ?? '−∞'
      const hi = p.max ?? '∞'
      parts.push(`${name}: ${lo}–${hi}`)
    } else if ((p.values ?? []).length > 0) {
      parts.push(`${name}: ${(p.values ?? []).join(', ')}`)
    }
  }
  return parts.join(' · ')
}

/** deriveGrants groups rules by (agent, upstream) into grants and attaches the purpose/date of the
 *  most-recent granted access-request for that pair. */
export function deriveGrants(rules: Rule[], requests: AccessRequest[]): Grant[] {
  const byPair = new Map<string, Grant>()
  const key = (a: string, u: string) => `${a} ${u}`
  for (const rule of rules) {
    const k = key(rule.subject_agent_id, rule.upstream_id)
    let g = byPair.get(k)
    if (!g) {
      g = { agentId: rule.subject_agent_id, upstreamId: rule.upstream_id, rules: [], purpose: '', grantedAt: '' }
      byPair.set(k, g)
    }
    g.rules.push(rule)
  }
  // Attach purpose from the newest granted request per pair (requests already arrive created_at DESC).
  for (const req of requests) {
    if (req.status !== 'granted') continue
    const g = byPair.get(key(req.agent_id, req.upstream_id))
    if (g && !g.purpose) {
      g.purpose = req.purpose
      g.grantedAt = req.resolved_at
    }
  }
  return [...byPair.values()]
}
