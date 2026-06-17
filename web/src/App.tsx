import { useCallback, useEffect, useState } from 'react'
import { Route, Routes } from 'react-router'
import { getVaultStatus, ApiError } from './lib/api'
import type { VaultStatus } from './lib/types'
import { useEventStore } from './lib/events'
import { Sidebar } from './components/Sidebar'
import { ToastContainer } from './components/Toast'
import { Unlock } from './pages/Unlock'
import { Dashboard } from './pages/Dashboard'
import { Upstreams } from './pages/Upstreams'
import { Agents } from './pages/Agents'
import { Rules } from './pages/Rules'
import { Approvals } from './pages/Approvals'

// Placeholder for routes Plan 6B fills in (Upstreams, Agents, Rules, Approvals, Audit, Settings).
function ComingSoon({ name }: { name: string }) {
  return (
    <div className="p-6">
      <h1 className="text-lg font-semibold">{name}</h1>
      <p className="mt-2 text-sm text-muted-foreground">Coming soon.</p>
    </div>
  )
}

export default function App() {
  const [status, setStatus] = useState<VaultStatus | null>(null)
  const [error, setError] = useState('')
  const connect = useEventStore((s) => s.connect)
  const disconnect = useEventStore((s) => s.disconnect)

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

  if (error && status === null) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background p-4">
        <div className="rounded-lg border border-destructive/40 bg-card px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      </div>
    )
  }

  if (status === null) {
    return <div className="flex min-h-screen items-center justify-center text-muted-foreground">…</div>
  }

  if (!status.initialized) {
    return <Unlock mode="init" onDone={refreshStatus} />
  }
  if (status.locked) {
    return <Unlock mode="unlock" onDone={refreshStatus} />
  }

  return (
    <div className="flex">
      <Sidebar />
      <main className="h-screen flex-1 overflow-y-auto">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/upstreams" element={<Upstreams />} />
          <Route path="/agents" element={<Agents />} />
          <Route path="/rules" element={<Rules />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/audit" element={<ComingSoon name="Audit" />} />
          <Route path="/settings" element={<ComingSoon name="Settings" />} />
        </Routes>
      </main>
      <ToastContainer />
    </div>
  )
}
