import { create } from 'zustand'
import { getOperatorSessionStatus, lockOperatorSession, openOperatorSession } from './api'

interface OperatorSessionState {
  open: boolean
  idleRemaining: number
  promptOpen: boolean
  /** The promise handed out by the in-flight `requirePrompt()` call, so concurrent 403s share one
   *  prompt/modal instead of each opening its own. Resolved (and cleared) by `unlock`/`dismissPrompt`. */
  pendingPrompt: Promise<boolean> | null
  promptResolver: ((opened: boolean) => void) | null
  refresh: () => Promise<void>
  unlock: (password: string) => Promise<void>
  lockNow: () => Promise<void>
  /** Opens the master-password prompt (or joins the already-open one) and resolves once the
   *  operator opens the session (`true`) or dismisses the prompt (`false`). This is the contract
   *  the api transport awaits before deciding whether to retry a gated call. */
  requirePrompt: () => Promise<boolean>
  dismissPrompt: () => void
}

/** Mirrors the daemon operator session so the shell can show a lock indicator + "Lock now", and the
 *  api transport can pop a master-password prompt when a privileged call hits the 403 gate. */
export const useOperatorSession = create<OperatorSessionState>((set, get) => ({
  open: false,
  idleRemaining: 0,
  promptOpen: false,
  pendingPrompt: null,
  promptResolver: null,
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
    const resolver = get().promptResolver
    set({
      open: s.open,
      idleRemaining: s.idle_remaining_seconds,
      promptOpen: false,
      pendingPrompt: null,
      promptResolver: null,
    })
    resolver?.(true)
  },
  async lockNow() {
    await lockOperatorSession()
    set({ open: false, idleRemaining: 0 })
  },
  requirePrompt() {
    const existing = get().pendingPrompt
    if (existing) return existing
    let resolve!: (opened: boolean) => void
    const promise = new Promise<boolean>((res) => {
      resolve = res
    })
    set({ promptOpen: true, pendingPrompt: promise, promptResolver: resolve })
    return promise
  },
  dismissPrompt() {
    const resolver = get().promptResolver
    set({ promptOpen: false, pendingPrompt: null, promptResolver: null })
    resolver?.(false)
  },
}))
