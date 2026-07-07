import { useState } from 'react'
import { ChevronRight, Pencil, Trash2 } from 'lucide-react'
import { deleteRule, renewRule, ApiError } from '../../lib/api'
import type { Rule } from '../../lib/types'
import { scopeOf, valueSummary, isExpired } from '../../lib/grants'
import { ScopeBadge } from './scope'
import { useToastStore } from '../../lib/toast'
import { ValueSetEditor, NumberRangeEditor } from './valueEditors'
import { RelTime } from '../../components/RelTime'
import { DurationSelect, DEFAULT_TTL_SECONDS } from './DurationSelect'

// segmentsOf splits a path template into fixed vs `{name:type}` variable pieces so the template can
// render variable segments visually distinct from the fixed structure.
function segmentsOf(template: string): { text: string; variable: boolean }[] {
  const out: { text: string; variable: boolean }[] = []
  const re = /\{(\w+):(\w+)\}/g
  let last = 0
  let m: RegExpExecArray | null
  while ((m = re.exec(template)) !== null) {
    if (m.index > last) out.push({ text: template.slice(last, m.index), variable: false })
    out.push({ text: m[0], variable: true })
    last = m.index + m[0].length
  }
  if (last < template.length) out.push({ text: template.slice(last), variable: false })
  return out
}

/** RuleRow renders one policy rule in the Access page's granted-rights section: collapsed it shows
 *  the scope badge, path (with variable segments as chips), a compact value-summary tail, the
 *  outcome, and edit/delete icons. A chevron expands per-variable value-set editors (text and
 *  number only — enum editing was dropped, see valueEditors.tsx). */
export function RuleRow({ rule, onChanged }: { rule: Rule; onChanged: () => void }) {
  const [open, setOpen] = useState(false)
  const [renewing, setRenewing] = useState(false)
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)
  const push = useToastStore((s) => s.push)
  const scope = scopeOf(rule)
  const summary = valueSummary(rule)
  const pols = rule.op_value_policies ?? {}
  const textVars = Object.entries(pols).filter(([, p]) => p.type === 'text')
  const numberVars = Object.entries(pols).filter(([, p]) => p.type === 'number')
  const expired = isExpired(rule)

  async function remove() {
    try {
      await deleteRule(rule.id)
      push('success', 'Operation deleted')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to delete operation')
    }
  }

  async function renew() {
    try {
      await renewRule(rule.id, ttl)
      push('success', 'Grant renewed')
      setRenewing(false)
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to renew')
    }
  }

  const hasEditors = textVars.length + numberVars.length > 0

  return (
    <div className="rounded-md border border-border bg-card">
      <div className="flex items-center gap-2.5 px-2.5 py-1.5">
        {hasEditors && (
          <button
            onClick={() => setOpen((o) => !o)}
            aria-label={open ? 'collapse rule' : 'expand rule'}
            className="text-muted-foreground hover:text-foreground"
          >
            <ChevronRight size={13} className={open ? 'rotate-90 transition' : 'transition'} />
          </button>
        )}
        <ScopeBadge scope={scope} />
        <span className="font-mono text-[12.5px] font-semibold">
          {rule.op_path_template ? (
            segmentsOf(rule.op_path_template).map((s, i) => (
              <span key={i} className={s.variable ? 'rounded bg-primary/15 px-1 text-primary' : ''}>
                {s.text}
              </span>
            ))
          ) : (
            resourceLabel(rule)
          )}
        </span>
        {summary && <span className="ml-1 text-[11px] text-muted-foreground">· {summary}</span>}
        <div className="ml-auto flex items-center gap-1">
          {rule.expires_at &&
            (expired ? (
              <span className="rounded bg-destructive/15 px-1.5 text-[11px] text-destructive">истекло</span>
            ) : (
              <span className="text-[11px] text-muted-foreground" title="expires">
                истекает <RelTime iso={rule.expires_at} />
              </span>
            ))}
          {(expired || rule.expires_at) && (
            <button
              onClick={() => setRenewing((v) => !v)}
              aria-label="Продлить"
              className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground text-[11px]"
            >
              Продлить
            </button>
          )}
          <span className="mr-1 text-[11px] text-success">{rule.outcome}</span>
          {hasEditors && (
            <button
              onClick={() => setOpen((o) => !o)}
              aria-label="edit rule"
              className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <Pencil size={13} />
            </button>
          )}
          <button
            onClick={remove}
            aria-label={`delete rule ${rule.id}`}
            className="rounded p-1 text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
          >
            <Trash2 size={13} />
          </button>
        </div>
      </div>
      {open && hasEditors && (
        <div className="space-y-2 border-t border-border px-2.5 py-2">
          {textVars.map(([name, p]) => (
            <ValueSetEditor key={name} ruleID={rule.id} varName={name} policy={p} onChange={onChanged} />
          ))}
          {numberVars.map(([name, p]) => (
            <NumberRangeEditor key={name} ruleID={rule.id} varName={name} policy={p} onChange={onChanged} />
          ))}
        </div>
      )}
      {renewing && (
        <div className="flex items-center gap-2 border-t border-border px-2.5 py-2">
          <DurationSelect value={ttl} onChange={setTtl} />
          <button onClick={renew} aria-label="ОК" className="rounded bg-primary px-2 py-1 text-[12px] text-primary-foreground">
            ОК
          </button>
        </div>
      )}
    </div>
  )
}

// resourceLabel renders a compact resource string for non-http rules (server-profile, k8s, browse).
function resourceLabel(rule: Rule): string {
  if (rule.profile === 'citeck') {
    const pp = rule.profile_params ?? {}
    return `Records · source ${pp['source_id'] ?? '*'} · ws ${pp['workspace'] ?? '*'}`
  }
  if (rule.namespace || rule.resource || rule.verb) return `${rule.namespace || '*'}/${rule.resource || '*'}`
  if (rule.browse_path) return rule.browse_path
  return rule.op_path_template ?? ''
}
