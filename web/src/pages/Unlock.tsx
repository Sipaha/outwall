import { useState } from 'react'
import { Lock } from 'lucide-react'
import { ApiError, vaultInit, vaultUnlock } from '../lib/api'

interface UnlockProps {
  /** "init" sets a new master password (with confirm); "unlock" opens an existing vault. */
  mode: 'init' | 'unlock'
  /** Called after a successful init/unlock so App can re-check vault status. */
  onDone: () => void
}

const inputClass =
  'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

export function Unlock({ mode, onDone }: UnlockProps) {
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const init = mode === 'init'

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (init && password !== confirm) {
      setError('Passwords do not match')
      return
    }
    if (!password) {
      setError('Password is required')
      return
    }
    setBusy(true)
    try {
      if (init) await vaultInit(password)
      else await vaultUnlock(password)
      onDone()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <form
        onSubmit={submit}
        className="w-[360px] max-w-[90vw] rounded-lg border border-border bg-card p-6 shadow-xl"
      >
        <div className="mb-4 flex items-center gap-2">
          <Lock size={16} className="text-primary" />
          <span className="font-mono text-sm font-semibold tracking-tight">outwall</span>
        </div>
        <h1 className="mb-1 text-base font-semibold">
          {init ? 'Set master password' : 'Unlock vault'}
        </h1>
        <p className="mb-4 text-xs text-muted-foreground">
          {init
            ? 'Choose a master password. It encrypts every upstream secret and is never stored.'
            : 'Enter your master password to decrypt upstream secrets.'}
        </p>

        <label className="mb-1 block text-xs font-medium">Master password</label>
        <input
          type="password"
          autoFocus
          className={inputClass}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-label="Master password"
        />

        {init && (
          <>
            <label className="mb-1 mt-3 block text-xs font-medium">Confirm password</label>
            <input
              type="password"
              className={inputClass}
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              aria-label="Confirm password"
            />
          </>
        )}

        {error && <div className="mt-3 text-[11px] text-destructive">{error}</div>}

        <button
          type="submit"
          disabled={busy}
          className="mt-4 w-full rounded bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          {busy ? '…' : init ? 'Set password' : 'Unlock'}
        </button>
      </form>
    </div>
  )
}
