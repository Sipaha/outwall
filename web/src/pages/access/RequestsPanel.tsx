import { useState } from 'react'
import { Clock } from 'lucide-react'
import { resolveApproval, ApiError } from '../../lib/api'
import type { Approval, ResolveOptions } from '../../lib/types'
import { ApprovalCard } from './ApprovalCards'
import { Modal } from '../../components/Modal'
import { FormField, fieldControlClass } from '../../components/FormField'
import { useToastStore } from '../../lib/toast'

/**
 * RequestsPanel is the "Запросы прав" section of the Access page: the aggregated pending-approval
 * queue (host/operation/preset/k8s/new-value cards from ApprovalCards.tsx). It receives `approvals`
 * from the parent's single fetch and calls `onChanged()` after a resolve so the parent reloads
 * rules+requests+approvals together, rather than owning its own fetch/poll loop.
 */
export function RequestsPanel({
  approvals,
  onChanged,
}: {
  approvals: Approval[]
  onChanged: () => void
}) {
  // Deny-with-reason: clicking Deny opens this modal; the (optional) reason is sent to the agent.
  const [denyId, setDenyId] = useState<string | null>(null)
  const [denyReason, setDenyReason] = useState('')
  const push = useToastStore((s) => s.push)

  async function decide(id: string, approve: boolean, opts?: ResolveOptions) {
    try {
      // Pass opts only when present so a plain approve stays a 2-arg call (no { trust_any: undefined }).
      await (opts ? resolveApproval(id, approve, opts) : resolveApproval(id, approve))
      push('success', approve ? 'Request approved' : 'Request denied')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to resolve')
    }
  }

  function confirmDeny(e?: React.FormEvent) {
    e?.preventDefault()
    const id = denyId
    setDenyId(null)
    if (id) void decide(id, false, denyReason.trim() ? { reason: denyReason.trim() } : undefined)
  }

  return (
    <section className="space-y-2">
      <header className="flex items-center gap-2">
        <Clock size={15} className="text-warning" />
        <span className="text-[13px] font-semibold">Запросы прав</span>
        <span className="rounded-full border border-warning/50 px-2 text-[11px] text-warning">
          {approvals.length}
        </span>
      </header>
      {approvals.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
          Нет запросов прав
        </div>
      ) : (
        <div className="space-y-2">
          {approvals.map((a) => (
            <ApprovalCard
              key={a.id}
              approval={a}
              onResolve={(id, approve, opts) => {
                // Deny routes through the reason modal; approve goes straight through.
                if (!approve) {
                  setDenyId(id)
                  setDenyReason('')
                } else void decide(id, true, opts)
              }}
            />
          ))}
        </div>
      )}

      <Modal
        open={denyId !== null}
        title="Deny request"
        onClose={() => setDenyId(null)}
        onSubmit={confirmDeny}
        footer={
          <>
            <button
              type="button"
              onClick={() => setDenyId(null)}
              className="rounded bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button type="submit" className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90">
              Deny
            </button>
          </>
        }
      >
        <FormField label="Reason (optional — shown to the agent)">
          <textarea
            className={fieldControlClass}
            rows={3}
            value={denyReason}
            onChange={(e) => setDenyReason(e.target.value)}
            aria-label="Deny reason"
            autoFocus
          />
        </FormField>
      </Modal>
    </section>
  )
}
