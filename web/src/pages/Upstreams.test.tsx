import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'
import { Upstreams } from './Upstreams'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Hosts> (Upstreams.tsx)', () => {
  it('renders hosts with credential status (set / none)', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u1', name: 'api.example.test', base_url: 'https://api.example.test', auth_type: 'static' },
      { id: 'u2', name: 'plain.test', base_url: 'https://plain.test', auth_type: 'none' },
    ])
    render(<Upstreams />)

    const withCred = (await screen.findByText('api.example.test')).closest('tr')!
    expect(within(withCred).getByText(/credential set/i)).toBeInTheDocument()

    const noCred = screen.getByText('plain.test').closest('tr')!
    expect(within(noCred).getByText(/no credential/i)).toBeInTheDocument()
  })

  it('sets a credential on an existing host via setUpstreamAuth', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u2', name: 'plain.test', base_url: 'https://plain.test', auth_type: 'none' },
    ])
    const setSpy = vi.spyOn(api, 'setUpstreamAuth').mockResolvedValue({ ok: true })
    render(<Upstreams />)

    fireEvent.click(await screen.findByRole('button', { name: 'Set credential for plain.test' }))

    fireEvent.change(await screen.findByLabelText('Header'), { target: { value: 'Authorization' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: 'Bearer y' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(setSpy).toHaveBeenCalledWith('plain.test', {
        type: 'static',
        header: 'Authorization',
        token: 'Bearer y',
      }),
    )
  })

  it('removes a host via deleteUpstream', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u2', name: 'plain.test', base_url: 'https://plain.test', auth_type: 'none' },
    ])
    const delSpy = vi.spyOn(api, 'deleteUpstream').mockResolvedValue({ ok: true })
    render(<Upstreams />)

    fireEvent.click(await screen.findByRole('button', { name: 'Remove plain.test' }))
    // Confirm in the modal.
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(delSpy).toHaveBeenCalledWith('plain.test'))
  })

  it('submits createUpstream from the add-host modal', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createUpstream').mockResolvedValue({ id: 'new' })
    render(<Upstreams />)

    fireEvent.click(screen.getByRole('button', { name: 'Add host' }))

    expect(screen.queryByLabelText('Header')).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'gh' } })
    fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'https://api.github.com' } })

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

  it('starts an OIDC browser login from the host row', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      {
        id: 'o1',
        name: 'oidc.test',
        base_url: 'https://oidc.test',
        auth_type: 'oidc-authorization-code',
      },
    ])
    const loginSpy = vi
      .spyOn(api, 'oauthLogin')
      .mockResolvedValue({ url: 'https://idp/authorize?x=1', opened: false })
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    render(<Upstreams />)

    fireEvent.click(await screen.findByRole('button', { name: 'Log in to oidc.test' }))
    await waitFor(() => expect(loginSpy).toHaveBeenCalledWith('oidc.test'))
    await waitFor(() => expect(openSpy).toHaveBeenCalledWith('https://idp/authorize?x=1', '_blank', 'noopener'))
  })

  it('shows sigv4 fields and submits them', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createUpstream').mockResolvedValue({ id: 'new' })
    render(<Upstreams />)

    fireEvent.click(screen.getByRole('button', { name: 'Add host' }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'aws' } })
    fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'https://api.aws' } })
    fireEvent.change(screen.getByDisplayValue('None'), { target: { value: 'sigv4' } })

    fireEvent.change(await screen.findByLabelText('AWS access key ID'), { target: { value: 'AKID' } })
    fireEvent.change(screen.getByLabelText('AWS secret access key'), { target: { value: 'SECRET' } })
    fireEvent.change(screen.getByLabelText('AWS region'), { target: { value: 'us-east-1' } })
    fireEvent.change(screen.getByLabelText('AWS service'), { target: { value: 'execute-api' } })

    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith('aws', 'https://api.aws', {
        type: 'sigv4',
        aws_access_key_id: 'AKID',
        aws_secret_access_key: 'SECRET',
        aws_region: 'us-east-1',
        aws_service: 'execute-api',
      }),
    )
  })
})
