import { useEffect, useState } from 'react'
import { Modal } from './Modal'
import { ApiError } from '../lib/api'
import { useOperatorSession } from '../lib/operatorSession'

const inputClass =
  'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

/** Master-password prompt shown when a privileged action hits the operator-session gate (or when the
 *  operator explicitly re-opens the session). Opening it does NOT unlock the vault — it authorizes
 *  privileged operator mutations for the idle-TTL window. */
export function OperatorSessionModal() {
  const promptOpen = useOperatorSession((s) => s.promptOpen)
  const dismiss = useOperatorSession((s) => s.dismissPrompt)
  const unlock = useOperatorSession((s) => s.unlock)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // This modal is mounted for the app's whole lifetime, so its local state must not linger past a
  // close — in particular the master password should not sit in memory once the prompt is gone,
  // whether it closed via a successful unlock or the operator dismissing it.
  useEffect(() => {
    if (!promptOpen) {
      setPassword('')
      setError('')
      setBusy(false)
    }
  }, [promptOpen])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (!password) {
      setError('Master password is required')
      return
    }
    setBusy(true)
    try {
      await unlock(password)
      setPassword('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={promptOpen}
      title="Operator session"
      width="sm"
      onClose={dismiss}
      onSubmit={submit}
      footer={
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          {busy ? '…' : 'Open session'}
        </button>
      }
    >
      <p className="text-xs text-muted-foreground">
        Privileged operator actions require your master password. This authorizes them for a short
        idle window and does not change the vault lock state.
      </p>
      <input
        type="password"
        autoFocus
        className={inputClass}
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        aria-label="Master password"
      />
      {error && <div className="text-[11px] text-destructive">{error}</div>}
    </Modal>
  )
}
