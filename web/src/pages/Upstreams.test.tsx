import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Upstreams } from './Upstreams'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Upstreams>', () => {
  it('renders rows from listUpstreams', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u1', name: 'github', base_url: 'https://api.github.com', auth_type: 'static' },
    ])
    render(<Upstreams />)
    expect(await screen.findByText('github')).toBeInTheDocument()
    expect(screen.getByText('https://api.github.com')).toBeInTheDocument()
  })

  it('shows conditional auth fields when the type changes and submits createUpstream', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createUpstream').mockResolvedValue({ id: 'new' })
    render(<Upstreams />)

    fireEvent.click(screen.getByRole('button', { name: 'Add upstream' }))

    // none → no header/value fields yet.
    expect(screen.queryByLabelText('Header')).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'gh' } })
    fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'https://api.github.com' } })

    // Switch to static → conditional fields appear.
    fireEvent.change(screen.getByDisplayValue('None'), { target: { value: 'static' } })
    expect(await screen.findByLabelText('Header')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Header'), { target: { value: 'Authorization' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: 'Bearer x' } })

    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith('gh', 'https://api.github.com', {
        type: 'static',
        header: 'Authorization',
        token: 'Bearer x',
      }),
    )
  })
})
