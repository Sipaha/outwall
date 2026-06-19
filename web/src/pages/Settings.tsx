import { useCallback, useEffect, useState } from 'react'
import {
  getVaultStatus,
  vaultLock,
  pruneAudit,
  getAuditRetention,
  setAuditRetention,
  ApiError,
} from '../lib/api'
import type { VaultStatus } from '../lib/types'
import { FormField, fieldControlClass } from '../components/FormField'
import { useToastStore } from '../lib/toast'

export function Settings() {
  const [status, setStatus] = useState<VaultStatus | null>(null)
  const [days, setDays] = useState(30)
  const [retentionDays, setRetentionDays] = useState(0)
  const [busy, setBusy] = useState(false)
  const [savingRetention, setSavingRetention] = useState(false)
  const push = useToastStore((s) => s.push)

  const load = useCallback(() => {
    getVaultStatus()
      .then((s) => setStatus(s))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load status')
      })
    getAuditRetention()
      .then(({ days }) => setRetentionDays(days))
      .catch(() => {
        /* retention is best-effort; a failure leaves the default 0 (keep all) */
      })
  }, [push])

  async function saveRetention() {
    setSavingRetention(true)
    try {
      const { days } = await setAuditRetention(retentionDays)
      setRetentionDays(days)
      push('success', days === 0 ? 'Auto-prune disabled (keep all)' : `Auto-prune set to ${days} days`)
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to save retention')
    } finally {
      setSavingRetention(false)
    }
  }

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

          <div className="border-t border-border/60 pt-3">
            <FormField label="Auto-prune: keep entries for (days, 0 = keep all)">
              <input
                className={fieldControlClass}
                type="number"
                min={0}
                value={retentionDays}
                onChange={(e) => setRetentionDays(Math.max(0, Number(e.target.value) || 0))}
                aria-label="Retention days"
              />
            </FormField>
            <button
              onClick={saveRetention}
              disabled={savingRetention}
              className="mt-2 rounded bg-muted px-3 py-1.5 text-xs font-medium text-foreground hover:opacity-90 disabled:opacity-50"
            >
              {savingRetention ? '…' : 'Save auto-prune'}
            </button>
            <p className="mt-2 text-[11px] text-muted-foreground">
              The daemon enforces this hourly in the background — old entries are deleted automatically.
            </p>
          </div>
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
