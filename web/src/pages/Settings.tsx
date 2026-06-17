import { useCallback, useEffect, useState } from 'react'
import { getVaultStatus, vaultLock, pruneAudit, ApiError } from '../lib/api'
import type { VaultStatus } from '../lib/types'
import { FormField, fieldControlClass } from '../components/FormField'
import { useToastStore } from '../lib/toast'

export function Settings() {
  const [status, setStatus] = useState<VaultStatus | null>(null)
  const [days, setDays] = useState(30)
  const [busy, setBusy] = useState(false)
  const push = useToastStore((s) => s.push)

  const load = useCallback(() => {
    getVaultStatus()
      .then((s) => setStatus(s))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load status')
      })
  }, [push])

  useEffect(load, [load])

  async function lock() {
    try {
      await vaultLock()
      // The app re-checks vault status on reload and falls back to the Unlock screen.
      window.location.reload()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to lock vault')
    }
  }

  async function prune() {
    setBusy(true)
    try {
      const cutoff = new Date(Date.now() - days * 864e5).toISOString()
      const { deleted } = await pruneAudit(cutoff)
      push('success', `Pruned ${deleted} audit ${deleted === 1 ? 'entry' : 'entries'}`)
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to prune audit')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Settings</h1>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Vault
        </header>
        <div className="space-y-3 px-4 py-3">
          <div className="text-xs text-muted-foreground">
            {status
              ? `Initialized: ${status.initialized ? 'yes' : 'no'} · Locked: ${status.locked ? 'yes' : 'no'}`
              : '…'}
          </div>
          <button
            onClick={lock}
            disabled={!status || status.locked}
            className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90 disabled:opacity-50"
          >
            Lock vault
          </button>
        </div>
      </section>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Audit retention
        </header>
        <div className="space-y-3 px-4 py-3">
          <FormField label="Prune entries older than (days)">
            <input
              className={fieldControlClass}
              type="number"
              min={1}
              value={days}
              onChange={(e) => setDays(Number(e.target.value) || 1)}
              aria-label="Days"
            />
          </FormField>
          <button
            onClick={prune}
            disabled={busy}
            className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
          >
            {busy ? '…' : 'Prune now'}
          </button>
        </div>
      </section>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Daemon
        </header>
        <div className="px-4 py-3 text-xs text-muted-foreground">
          outwall binds to localhost only — the control, data, and UI planes are never exposed off
          the loopback interface.
        </div>
      </section>
    </div>
  )
}
