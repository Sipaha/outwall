import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Approvals } from './Approvals'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Approvals>', () => {
  it('approves a pending approval via resolveApproval(id, true)', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'p1',
        agent_id: 'agent-1234',
        upstream_id: 'up-1234',
        method: 'GET',
        path: '/repos',
        purpose: 'fetch repos',
        created_at: '2026-06-17T10:00:00Z',
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
    const resolveSpy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })

    render(<Approvals />)

    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }))

    await waitFor(() => expect(resolveSpy).toHaveBeenCalledWith('p1', true))
  })
})
