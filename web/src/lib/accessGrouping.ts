import { create } from 'zustand'

interface AccessGroupingState {
  by: 'agent' | 'upstream'
  setBy: (by: 'agent' | 'upstream') => void
}

/** In-memory store for the Access page's grouping toggle (by agent / by upstream). */
export const useAccessGrouping = create<AccessGroupingState>((set) => ({
  by: 'agent',
  setBy: (by) => set({ by }),
}))
