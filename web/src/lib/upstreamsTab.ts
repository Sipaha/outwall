import { create } from 'zustand'

interface UpstreamsTabState {
  tab: string
  setTab: (t: string) => void
}

/** In-memory store for the selected Upstreams sub-tab (HTTP / Citeck / Kubernetes) so it survives
 *  the page unmounting when the operator navigates away via the sidebar and back. A full page
 *  reload resets to 'http' — that's fine, only the left-nav round-trip needs to be preserved. */
export const useUpstreamsTab = create<UpstreamsTabState>((set) => ({
  tab: 'http',
  setTab: (tab) => set({ tab }),
}))
