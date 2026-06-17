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

  it('renders the k8s tuple and the patch body for a k8s approval', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'p2',
        agent_id: 'agent-9999',
        upstream_id: 'up-9999',
        method: 'PATCH',
        path: '/api/v1/namespaces/prod/deployments/web',
        purpose: 'bump image',
        created_at: '2026-06-18T10:00:00Z',
        namespace: 'prod',
        resource: 'deployments',
        verb: 'patch',
        request_body: '{"spec":{"image":"web:v2"}}',
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])

    render(<Approvals />)

    // The parsed tuple is shown.
    expect(await screen.findByText('prod')).toBeInTheDocument()
    expect(screen.getByText('deployments')).toBeInTheDocument()
    expect(screen.getByText('patch')).toBeInTheDocument()
    // The patch body (the change) is rendered.
    expect(screen.getByText(/"image":"web:v2"/)).toBeInTheDocument()
  })
})
