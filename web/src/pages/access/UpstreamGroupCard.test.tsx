import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { UpstreamGroupCard } from './UpstreamGroupCard'
import * as api from '../../lib/api'
import type { Grant } from '../../lib/grants'
import type { Agent, Upstream } from '../../lib/types'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const up: Upstream = { id: 'up1', name: 'gitlab.example.com', base_url: '', auth_type: '', kind: 'http' }
const agents: Agent[] = [
  { id: 'ag1', name: 'claude', status: 'active', created_at: '', last_seen_at: '' },
  { id: 'ag2', name: 'codex', status: 'active', created_at: '', last_seen_at: '' },
]
const grants: Grant[] = [
  {
    agentId: 'ag1', upstreamId: 'up1', purpose: '', grantedAt: '',
    rules: [{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET', op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }],
  },
  {
    agentId: 'ag2', upstreamId: 'up1', purpose: '', grantedAt: '',
    rules: [{ id: 'r2', subject_agent_id: 'ag2', upstream_id: 'up1', op_method: 'GET', op_path_template: '/y', outcome: 'allow', rate_limit_per_min: 0 }],
  },
]

describe('<UpstreamGroupCard>', () => {
  it('shows the upstream as the container with each agent nested inside', () => {
    render(<UpstreamGroupCard upstreamId="up1" upstream={up} grants={grants} agents={agents} onChanged={() => {}} />)
    expect(screen.getByText('gitlab.example.com')).toBeInTheDocument() // upstream header (once)
    expect(screen.getByText('claude')).toBeInTheDocument() // nested agent
    expect(screen.getByText('codex')).toBeInTheDocument() // nested agent
    expect(screen.getByText(/агентов/)).toBeInTheDocument() // agent count in the header
  })

  it('revokes a specific agent’s grant on this upstream', async () => {
    const spy = vi.spyOn(api, 'revokeGrant').mockResolvedValue({ ok: true, rules_removed: 1 })
    const onChanged = vi.fn()
    render(<UpstreamGroupCard upstreamId="up1" upstream={up} grants={grants} agents={agents} onChanged={onChanged} />)
    // Two Revoke buttons (one per agent); click the first → revokes ag1 on up1.
    fireEvent.click(screen.getAllByRole('button', { name: 'Revoke' })[0])
    await waitFor(() => expect(spy).toHaveBeenCalledWith('ag1', 'up1'))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
