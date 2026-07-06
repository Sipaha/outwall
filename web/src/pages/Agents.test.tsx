import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Agents } from './Agents'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Agents>', () => {
  it('shows "Never" for an agent with no last_seen_at, and a formatted time otherwise', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([
      {
        id: 'agent-1',
        name: 'claude',
        status: 'active',
        created_at: '2026-06-17T10:00:00Z',
        last_seen_at: '',
      },
      {
        id: 'agent-2',
        name: 'codex',
        status: 'active',
        created_at: '2026-06-17T10:00:00Z',
        last_seen_at: '2026-06-18T12:30:00Z',
      },
    ])
    vi.spyOn(api, 'listRules').mockResolvedValue([])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])

    render(<Agents />)

    await screen.findByText('claude')
    expect(screen.getByText('Never')).toBeInTheDocument()
    expect(screen.queryByText('2026-06-18T12:30:00Z')).not.toBeInTheDocument()
  })

  it('deletes an agent immediately on click (no confirmation)', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([
      {
        id: 'agent-1',
        name: 'claude',
        status: 'active',
        created_at: '2026-06-17T10:00:00Z',
        last_seen_at: '',
      },
    ])
    vi.spyOn(api, 'listRules').mockResolvedValue([])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
    const deleteSpy = vi.spyOn(api, 'deleteAgent').mockResolvedValue({ ok: true })

    render(<Agents />)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete agent claude' }))

    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith('agent-1'))
  })
})
