import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { Access } from './Access'
import * as api from '../lib/api'
import { useAccessGrouping } from '../lib/accessGrouping'

afterEach(() => { cleanup(); vi.restoreAllMocks(); useAccessGrouping.setState({ by: 'agent' }) })

function seed() {
  vi.spyOn(api, 'listAgents').mockResolvedValue([
    { id: 'ag1', name: 'claude', status: 'active', created_at: '2026-06-17T10:00:00Z', last_seen_at: '' },
    { id: 'ag2', name: 'lonely', status: 'active', created_at: '2026-06-17T10:00:00Z', last_seen_at: '' },
  ])
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([{ id: 'up1', name: 'gitlab', base_url: '', auth_type: '', kind: 'http' }])
  vi.spyOn(api, 'listRules').mockResolvedValue([{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET', op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }])
  vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
  vi.spyOn(api, 'listApprovals').mockResolvedValue([])
}

describe('<Access>', () => {
  it('renders the requests panel and the agent grant group', async () => {
    seed()
    render(<MemoryRouter><Access /></MemoryRouter>)
    await screen.findByText('Запросы прав')
    expect(await screen.findByText('claude')).toBeInTheDocument()
    expect(screen.getByText('gitlab')).toBeInTheDocument()
    expect(screen.getByText('Выданные права')).toBeInTheDocument()
  })
  it('switches grouping to upstream', async () => {
    seed()
    render(<MemoryRouter><Access /></MemoryRouter>)
    await screen.findByText('claude')
    fireEvent.click(screen.getByText('По upstream'))
    await waitFor(() => expect(screen.getByText('gitlab')).toBeInTheDocument())
  })
  it('still shows a grant-less agent (with its delete button) in by-agent mode', async () => {
    seed()
    render(<MemoryRouter><Access /></MemoryRouter>)
    await screen.findByText('claude')
    fireEvent.click(screen.getByText('По агенту')) // grouping store persists across tests; force by-agent
    expect(await screen.findByText('lonely')).toBeInTheDocument()
    expect(screen.getByLabelText('Delete agent lonely')).toBeInTheDocument()
    expect(screen.queryByText('Прав ещё не выдано — действует запрет по умолчанию')).not.toBeInTheDocument()
  })
})
