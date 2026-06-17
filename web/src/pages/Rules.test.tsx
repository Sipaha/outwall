import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Rules } from './Rules'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Rules>', () => {
  it('resolves agent and upstream names for rows', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([
      {
        id: 'r1',
        subject_agent_id: 'a1',
        upstream_id: 'u1',
        method: '*',
        path_glob: '/**',
        outcome: 'allow',
        rate_limit_per_min: 0,
      },
    ])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u1', name: 'github', base_url: 'https://api.github.com', auth_type: 'none' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([{ id: 'a1', name: 'claude', status: 'active' }])

    render(<Rules />)

    // Names are resolved into table cells (the closed add-modal also lists them as <option>s).
    expect(await screen.findByRole('cell', { name: 'claude' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'github' })).toBeInTheDocument()
    expect(screen.getByText('∞')).toBeInTheDocument()
  })

  it('submits createRule from the add-rule modal', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u1', name: 'github', base_url: 'https://api.github.com', auth_type: 'none' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'new' })

    render(<Rules />)
    await screen.findByText('No rules yet — default-deny applies')

    fireEvent.click(screen.getByRole('button', { name: 'Add rule' }))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith({
        subject_agent_id: '',
        upstream_id: 'u1',
        method: '*',
        path_glob: '/**',
        outcome: 'allow',
        rate_limit_per_min: 0,
      }),
    )
  })
})
