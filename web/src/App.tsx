import { useCallback, useEffect, useState } from 'react'
import { Route, Routes, useNavigate } from 'react-router'
import { getVaultStatus, ApiError, setSessionRequiredHandler } from './lib/api'
import type { VaultStatus } from './lib/types'
import { useEventStore } from './lib/events'
import { useOperatorSession } from './lib/operatorSession'
import { Sidebar } from './components/Sidebar'
import { ToastContainer } from './components/Toast'
import { OperatorSessionModal } from './components/OperatorSessionModal'
import { Unlock } from './pages/Unlock'
import { Dashboard } from './pages/Dashboard'
import { Upstreams } from './pages/Upstreams'
import { Agents } from './pages/Agents'
import { Rules } from './pages/Rules'
import { Approvals } from './pages/Approvals'
import { Audit } from './pages/Audit'
import { Settings } from './pages/Settings'

export default function App() {
  const [status, setStatus] = useState<VaultStatus | null>(null)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  const connect = useEventStore((s) => s.connect)
  const disconnect = useEventStore((s) => s.disconnect)
  const openApprovals = useEventStore((s) => s.counters['desktop.open-approvals'] ?? 0)
  const requirePrompt = useOperatorSession((s) => s.requirePrompt)
  const refreshSession = useOperatorSession((s) => s.refresh)

  const refreshStatus = useCallback(() => {
    // setState lives in the .then/.catch callbacks (deferred past the fetch) — the form the
    // react-hooks rule endorses for effects that sync with an external system.
    getVaultStatus()
      .then((s) => {
        setStatus(s)
        setError('')
      })
      .catch((err) => {
        setError(err instanceof ApiError ? err.message : 'Cannot reach the daemon')
      })
  }, [])

  // Check vault status on mount (and after an init/unlock via onDone).
  useEffect(refreshStatus, [refreshStatus])

  const unlocked = status !== null && status.initialized && !status.locked

  // Connect the SSE stream once the vault is open (the shell is showing); tear it down otherwise.
  useEffect(() => {
    if (unlocked) {
      connect()
      return () => disconnect()
    }
  }, [unlocked, connect, disconnect])

  // Let the api transport pop the master-password prompt on a 403 "operator session required",
  // and await the operator's decision so the transport can retry the gated call once (mirrors the
  // CLI's sudo-style doPrivileged — ADR-0041).
  useEffect(() => {
    setSessionRequiredHandler(requirePrompt)
    return () => setSessionRequiredHandler(null)
  }, [requirePrompt])

  // Reflect the daemon operator-session state once the shell is up.
  useEffect(() => {
    if (unlocked) refreshSession()
  }, [unlocked, refreshSession])

  // Navigate to the Approvals page whenever the desktop sends a "open-approvals" signal (e.g. on
  // notification click), so the operator lands on the pending request directly.
  useEffect(() => {
    if (openApprovals > 0) navigate('/approvals')
  }, [openApprovals, navigate])

  // The operator-session gate covers vault init/unlock too (a fresh daemon start has no open
  // session), so the master-password prompt must be mounted on every branch below — not just the
  // unlocked shell — or a gate 403 hit from the Unlock screen would have nowhere to render.
  let content: React.ReactNode
  if (error && status === null) {
    content = (
      <div className="flex min-h-screen items-center justify-center bg-background p-4">
        <div className="rounded-lg border border-destructive/40 bg-card px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      </div>
    )
  } else if (status === null) {
    content = <div className="flex min-h-screen items-center justify-center text-muted-foreground">…</div>
  } else if (!status.initialized) {
    content = <Unlock mode="init" onDone={refreshStatus} />
  } else if (status.locked) {
    content = <Unlock mode="unlock" onDone={refreshStatus} />
  } else {
    content = (
      <div className="flex">
        <Sidebar />
        <main className="h-screen flex-1 overflow-y-auto">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/upstreams" element={<Upstreams />} />
            <Route path="/agents" element={<Agents />} />
            <Route path="/rules" element={<Rules />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/audit" element={<Audit />} />
            <Route path="/settings" element={<Settings />} />
          </Routes>
        </main>
        <ToastContainer />
      </div>
    )
  }

  return (
    <>
      {content}
      <OperatorSessionModal />
    </>
  )
}
