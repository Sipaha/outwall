import { create } from 'zustand'
import type { OutwallEvent } from './types'

/**
 * The SSE event store. It opens a single EventSource('/api/events') and fans the daemon's
 * domain events (see internal/events + ADR-0005) into a per-type counter, so any screen can
 * `useEffect(..., [useEventStore(s => s.counters['approval.enqueued'])])` to re-fetch its data
 * when something changes — no polling.
 *
 * EventSource cannot set the X-Outwall-CSRF header, which is why the daemon exempts
 * GET /api/events from the CSRF gate (read-only, same-origin, loopback — see ADR-0005).
 */
interface EventState {
  connected: boolean
  lastEvent: OutwallEvent | null
  /** Per-event-type monotonically-increasing counter; bumped on each matching event. */
  counters: Record<string, number>
  connect: () => void
  disconnect: () => void
}

let source: EventSource | null = null

// The daemon's event taxonomy (ADR-0005). EventSource delivers named events via
// addEventListener(type), not onmessage, because the daemon writes `event: <type>`.
const EVENT_TYPES = [
  'agent.registered',
  'upstream.created',
  'rule.created',
  'vault.unlocked',
  'approval.enqueued',
  'approval.resolved',
  'access.requested',
  'audit.recorded',
  'desktop.open-approvals',
] as const

export const useEventStore = create<EventState>((set, get) => ({
  connected: false,
  lastEvent: null,
  counters: {},

  connect() {
    if (source) return // already connected (or connecting)
    const es = new EventSource(`${'/api'}/events`)
    source = es

    es.onopen = () => set({ connected: true })
    es.onerror = () => {
      // EventSource auto-reconnects; reflect the transient disconnect in the UI.
      set({ connected: false })
    }

    const handle = (type: string) => (msg: MessageEvent) => {
      let data: unknown = null
      try {
        data = msg.data ? JSON.parse(msg.data) : null
      } catch {
        /* ignore malformed payloads */
      }
      const counters = { ...get().counters, [type]: (get().counters[type] ?? 0) + 1 }
      set({ lastEvent: { type, data }, counters })
    }

    for (const type of EVENT_TYPES) {
      es.addEventListener(type, handle(type))
    }
  },

  disconnect() {
    if (source) {
      source.close()
      source = null
    }
    set({ connected: false })
  },
}))
