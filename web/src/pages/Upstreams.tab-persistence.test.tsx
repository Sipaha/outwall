import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { Upstreams } from './Upstreams'
import * as api from '../lib/api'
import { useUpstreamsTab } from '../lib/upstreamsTab'

// Mock Clusters so the k8s tab doesn't need its own listUpstreams/listAgents wiring.
vi.mock('./Clusters', () => ({ Clusters: () => <div>Clusters stub</div> }))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useUpstreamsTab.setState({ tab: 'http' })
})

describe('Upstreams sub-tab persistence', () => {
  it('keeps the selected sub-tab across unmount/remount (left-nav switch)', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])

    const { unmount } = render(<Upstreams />)
    fireEvent.click(await screen.findByRole('tab', { name: 'Kubernetes' }))
    expect(screen.getByRole('tab', { name: 'Kubernetes' })).toHaveAttribute('aria-selected', 'true')

    // Simulate navigating away via the sidebar (unmounts the page) and back (remounts it).
    unmount()
    render(<Upstreams />)

    expect(await screen.findByRole('tab', { name: 'Kubernetes' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
  })
})
