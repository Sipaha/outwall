import { useState } from 'react'
import { setRuleVariablePolicy, ApiError } from '../../lib/api'
import type { ValuePolicy } from '../../lib/types'
import { useToastStore } from '../../lib/toast'

interface ValueSetEditorProps {
  ruleID: string
  varName: string
  policy: ValuePolicy
  onChange: () => void
}

/** Per-text-variable editor: the allowed-value chips (each removable), an add-value input, and a
 *  "trust any value" toggle. Each action posts the whole recomputed policy via setRuleVariablePolicy. */
export function ValueSetEditor({ ruleID, varName, policy, onChange }: ValueSetEditorProps) {
  const [draft, setDraft] = useState('')
  const push = useToastStore((s) => s.push)
  const isAny = policy.mode === 'any'
  const values = policy.values ?? []

  async function post(next: ValuePolicy) {
    try {
      await setRuleVariablePolicy(ruleID, varName, next)
      onChange()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to update value policy')
    }
  }

  function addValue() {
    const v = draft.trim()
    if (v === '') return
    setDraft('')
    void post({ type: 'text', mode: 'set', values: [...values, v] })
  }

  function removeValue(v: string) {
    void post({ type: 'text', mode: 'set', values: values.filter((x) => x !== v) })
  }

  function toggleAny(any: boolean) {
    void post(any ? { type: 'text', mode: 'any' } : { type: 'text', mode: 'set', values })
  }

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs">
          {varName}
          <span className="ml-1 text-muted-foreground">:text</span>
        </span>
        <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <input
            type="checkbox"
            checked={isAny}
            onChange={(e) => toggleAny(e.target.checked)}
            aria-label={`Trust any value for ${varName}`}
          />
          trust any value
        </label>
      </div>
      {isAny ? (
        <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1 text-[11px] text-warning">
          ⚠ Any value is allowed for {varName}.
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          {values.length === 0 && (
            <span className="text-[11px] text-muted-foreground">No values yet — none allowed.</span>
          )}
          {values.map((v) => (
            <span
              key={v}
              className="inline-flex items-center gap-1 rounded bg-primary/15 px-1.5 py-0.5 font-mono text-[11px] text-primary"
            >
              {v}
              <button
                onClick={() => removeValue(v)}
                aria-label={`Remove ${v} from ${varName}`}
                className="text-primary/70 hover:text-primary"
              >
                ×
              </button>
            </span>
          ))}
          <input
            className="w-40 rounded border border-border bg-muted px-2 py-0.5 text-[11px] focus:border-primary focus:outline-none"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                addValue()
              }
            }}
            placeholder="add a value…"
            aria-label={`Value to add for ${varName}`}
          />
          <button
            onClick={addValue}
            aria-label={`Add value for ${varName}`}
            className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:text-foreground"
          >
            Add
          </button>
        </div>
      )}
    </div>
  )
}

/** Per-number-variable editor: an inclusive [min,max] range, or "any number". */
export function NumberRangeEditor({ ruleID, varName, policy, onChange }: ValueSetEditorProps) {
  const push = useToastStore((s) => s.push)
  const isAny = policy.mode === 'any'

  async function post(next: ValuePolicy) {
    try {
      await setRuleVariablePolicy(ruleID, varName, next)
      onChange()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to update value policy')
    }
  }

  function setBound(which: 'min' | 'max', raw: string) {
    const n = raw.trim() === '' ? undefined : Number(raw)
    if (n !== undefined && Number.isNaN(n)) return
    void post({ type: 'number', mode: 'range', min: which === 'min' ? n : policy.min, max: which === 'max' ? n : policy.max })
  }

  function toggleAny(any: boolean) {
    void post(any ? { type: 'number', mode: 'any' } : { type: 'number', mode: 'range', min: policy.min, max: policy.max })
  }

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs">
          {varName}
          <span className="ml-1 text-muted-foreground">:number</span>
        </span>
        <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <input
            type="checkbox"
            checked={isAny}
            onChange={(e) => toggleAny(e.target.checked)}
            aria-label={`Allow any number for ${varName}`}
          />
          any number
        </label>
      </div>
      {isAny ? (
        <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1 text-[11px] text-warning">
          ⚠ Any number is allowed for {varName}.
        </div>
      ) : (
        <div className="flex items-center gap-2 text-[11px]">
          <label className="flex items-center gap-1">
            min
            <input
              type="number"
              className="w-24 rounded border border-border bg-muted px-2 py-0.5 focus:border-primary focus:outline-none"
              defaultValue={policy.min ?? ''}
              onBlur={(e) => setBound('min', e.target.value)}
              aria-label={`Minimum for ${varName}`}
            />
          </label>
          <label className="flex items-center gap-1">
            max
            <input
              type="number"
              className="w-24 rounded border border-border bg-muted px-2 py-0.5 focus:border-primary focus:outline-none"
              defaultValue={policy.max ?? ''}
              onBlur={(e) => setBound('max', e.target.value)}
              aria-label={`Maximum for ${varName}`}
            />
          </label>
          <span className="text-muted-foreground">out-of-range → denied</span>
        </div>
      )}
    </div>
  )
}
