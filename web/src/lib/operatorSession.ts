import { create } from 'zustand'
import { getOperatorSessionStatus, lockOperatorSession, openOperatorSession } from './api'

interface OperatorSessionState {
  open: boolean
  idleRemaining: number
  promptOpen: boolean
  refresh: () => Promise<void>
  unlock: (password: string) => Promise<void>
  lockNow: () => Promise<void>
  requirePrompt: () => void
  dismissPrompt: () => void
}

/** Mirrors the daemon operator session so the shell can show a lock indicator + "Lock now", and the
 *  api transport can pop a master-password prompt when a privileged call hits the 403 gate. */
export const useOperatorSession = create<OperatorSessionState>((set) => ({
  open: false,
  idleRemaining: 0,
  promptOpen: false,
  async refresh() {
    try {
      const s = await getOperatorSessionStatus()
      set({ open: s.open, idleRemaining: s.idle_remaining_seconds })
    } catch {
      set({ open: false, idleRemaining: 0 })
    }
  },
  async unlock(password: string) {
    const s = await openOperatorSession(password)
    set({ open: s.open, idleRemaining: s.idle_remaining_seconds, promptOpen: false })
  },
  async lockNow() {
    await lockOperatorSession()
    set({ open: false, idleRemaining: 0 })
  },
  requirePrompt() {
    set({ promptOpen: true })
  },
  dismissPrompt() {
    set({ promptOpen: false })
  },
}))
