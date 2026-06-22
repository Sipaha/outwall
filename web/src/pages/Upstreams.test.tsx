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
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
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
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
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

  it('auto-fills OIDC endpoints via Discover', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
    const discoverSpy = vi.spyOn(api, 'discoverOIDC').mockResolvedValue({
      issuer: 'https://idp/realms/x',
      authorization_endpoint: 'https://idp/realms/x/auth',
      token_endpoint: 'https://idp/realms/x/token',
      scopes_supported: ['openid', 'profile'],
    })
    vi.spyOn(api, 'oidcRedirectURI').mockResolvedValue({
      redirect_uri: 'http://127.0.0.1:23312/callback',
    })
    render(<Upstreams />)

    fireEvent.click(await screen.findByRole('button', { name: 'Add host' }))
    // Switch auth type to OIDC authorization-code.
    fireEvent.change(screen.getByLabelText('Auth type'), {
      target: { value: 'oidc-authorization-code' },
    })
    // The fixed redirect URI to register in the IdP is shown.
    expect(await screen.findByText('http://127.0.0.1:23312/callback')).toBeInTheDocument()
    // Enter the issuer URL and click Discover.
    fireEvent.change(screen.getByLabelText('Issuer or discovery URL'), {
      target: { value: 'https://idp/realms/x' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Discover' }))

    await waitFor(() => expect(discoverSpy).toHaveBeenCalledWith('https://idp/realms/x'))
    // The endpoint fields are filled from the discovery document.
    await waitFor(() =>
      expect((screen.getByLabelText('Authorization URL') as HTMLInputElement).value).toBe(
        'https://idp/realms/x/auth',
      ),
    )
    expect((screen.getByLabelText('Token URL (auth-code)') as HTMLInputElement).value).toBe(
      'https://idp/realms/x/token',
    )
  })

  it('removes a host via deleteUpstream', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u2', name: 'plain.test', base_url: 'https://plain.test', auth_type: 'none' },
    ])
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
    const delSpy = vi.spyOn(api, 'deleteUpstream').mockResolvedValue({ ok: true })
    render(<Upstreams />)

    fireEvent.click(await screen.findByRole('button', { name: 'Remove plain.test' }))
    // Confirm in the modal.
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(delSpy).toHaveBeenCalledWith('plain.test'))
  })

  it('submits createUpstream from the add-host modal', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
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
      }, 'raw-http'),
    )
  })

  it('starts an OIDC browser login from the host row', async () => {
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
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
    vi.spyOn(api, 'getProfiles').mockResolvedValue([])
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
      }, 'raw-http'),
    )
  })

  it('submits the chosen server profile when adding a host', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'getProfiles').mockResolvedValue([
      { profile: 'citeck', fields: [] },
    ])
    const createSpy = vi.spyOn(api, 'createUpstream').mockResolvedValue({ id: 'new' })
    render(<Upstreams />)

    fireEvent.click(screen.getByRole('button', { name: 'Add host' }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'c' } })
    fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'https://c.test' } })
    // Wait for the profiles async load to populate the 'citeck' option before selecting it.
    const serverTypeSelect = await screen.findByLabelText('Server type') as HTMLSelectElement
    await waitFor(() => expect(serverTypeSelect.options.length).toBeGreaterThan(1))
    fireEvent.change(serverTypeSelect, { target: { value: 'citeck' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith('c', 'https://c.test', { type: 'none' }, 'citeck'),
    )
  })
})
