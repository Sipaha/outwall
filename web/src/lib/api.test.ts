import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, vaultUnlock, listAgents, openOperatorSession } from './api'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('api client', () => {
  it('vaultUnlock posts to /api/vault/unlock with NO CSRF header and a password body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ locked: false }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const res = await vaultUnlock('hunter2')
    expect(res).toEqual({ locked: false })

    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/vault/unlock')
    expect(opts.method).toBe('POST')
    // The static X-Outwall-CSRF model is retired — the operator-session gate replaced it.
    expect(opts.headers['X-Outwall-CSRF']).toBeUndefined()
    expect(opts.headers['Content-Type']).toBe('application/json')
    expect(JSON.parse(opts.body)).toEqual({ password: 'hunter2' })
  })

  it('openOperatorSession posts to /operator/session/open with the password body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ open: true, idle_remaining_seconds: 3600 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const res = await openOperatorSession('hunter2')
    expect(res).toEqual({ open: true, idle_remaining_seconds: 3600 })
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/operator/session/open')
    expect(opts.method).toBe('POST')
    expect(opts.headers['X-Outwall-CSRF']).toBeUndefined()
    expect(JSON.parse(opts.body)).toEqual({ password: 'hunter2' })
  })

  it('throws ApiError with status + daemon error message on a 401', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: 'incorrect master password' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(vaultUnlock('wrong')).rejects.toMatchObject({
      name: 'ApiError',
      status: 401,
      message: 'incorrect master password',
    })
    await expect(vaultUnlock('wrong')).rejects.toBeInstanceOf(ApiError)
  })

  it('GET helpers send NO CSRF header and parse JSON arrays', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([{ id: 'a1', name: 'claude', status: 'new' }]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const agents = await listAgents()
    expect(agents).toHaveLength(1)
    expect(agents[0].name).toBe('claude')
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/agents')
    expect(opts.headers['X-Outwall-CSRF']).toBeUndefined()
  })
})
