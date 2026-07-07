import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { UpstreamGrantCard } from './UpstreamGrantCard'
import * as api from '../../lib/api'
import type { Grant } from '../../lib/grants'
import type { Upstream } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const grant: Grant = {
  agentId: 'ag1', upstreamId: 'up1', purpose: 'CI', grantedAt: '2026-06-17T10:05:00Z',
  rules: [{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET',
    op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }],
}
const up: Upstream = { id: 'up1', name: 'gitlab.example.com', base_url: '', auth_type: '', kind: 'http' }

describe('<UpstreamGrantCard>', () => {
  it('renders the hostname and a rule', () => {
    render(<UpstreamGrantCard grant={grant} upstream={up} onChanged={() => {}} />)
    expect(screen.getByText('gitlab.example.com')).toBeInTheDocument()
    expect(screen.getByText('GET')).toBeInTheDocument()
  })
  it('revokes the grant', async () => {
    const spy = vi.spyOn(api, 'revokeGrant').mockResolvedValue({ ok: true, rules_removed: 1 })
    const onChanged = vi.fn()
    render(<UpstreamGrantCard grant={grant} upstream={up} onChanged={onChanged} />)
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(spy).toHaveBeenCalledWith('ag1', 'up1'))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
