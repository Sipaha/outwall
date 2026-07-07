import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { AgentCard } from './AgentCard'
import * as api from '../../lib/api'
import type { Agent } from '../../lib/types'
import type { Grant } from '../../lib/grants'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const agent: Agent = { id: 'ag1', name: 'claude', status: 'active', created_at: '2026-06-17T10:00:00Z', last_seen_at: '' }
const grants: Grant[] = [{ agentId: 'ag1', upstreamId: 'up1', purpose: '', grantedAt: '',
  rules: [{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET', op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }] }]
const ups = [{ id: 'up1', name: 'gitlab', base_url: '', auth_type: '', kind: 'http' }]

function renderCard() {
  return render(<MemoryRouter><AgentCard agent={agent} grants={grants} upstreams={ups} onChanged={() => {}} /></MemoryRouter>)
}

describe('<AgentCard>', () => {
  it('toggles collapse when the header body is clicked', () => {
    renderCard()
    expect(screen.getByText('gitlab')).toBeInTheDocument()      // expanded by default
    fireEvent.click(screen.getByText('claude'))
    expect(screen.queryByText('gitlab')).not.toBeInTheDocument() // collapsed
  })
  it('deletes the agent immediately from the trash icon (no modal, no toggle)', async () => {
    const spy = vi.spyOn(api, 'deleteAgent').mockResolvedValue({ ok: true })
    renderCard()
    fireEvent.click(screen.getByRole('button', { name: 'Delete agent claude' }))
    await waitFor(() => expect(spy).toHaveBeenCalledWith('ag1'))
    expect(screen.getByText('gitlab')).toBeInTheDocument()       // still expanded → header didn't toggle
  })
})
