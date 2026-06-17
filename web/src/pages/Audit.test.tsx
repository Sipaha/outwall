import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Audit } from './Audit'
import * as api from '../lib/api'

const entry = {
  id: 'a1',
  ts: '2026-06-17T10:00:00Z',
  agent_id: 'agent-1',
  agent_name: 'claude',
  upstream_id: 'up-1',
  upstream_name: 'github',
  method: 'GET',
  path: '/repos',
  query: 'page=1',
  status_code: 200,
  duration_ms: 42,
  req_bytes: 0,
  resp_bytes: 128,
  decision: 'allow',
  rule_id: 'r1',
  error: '',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Audit>', () => {
  it('loads the journal and fetches the body on row view', async () => {
    vi.spyOn(api, 'listAudit').mockResolvedValue([entry])
    const getSpy = vi.spyOn(api, 'getAudit').mockResolvedValue({
      ...entry,
      headers: { authorization: '***masked***' },
      bodies: [
        { kind: 'response', content_type: 'application/json', size: 13, sha256: 'abc', truncated: false, body: '{"ok":true}' },
      ],
    })

    render(<Audit />)
    expect(await screen.findByRole('cell', { name: 'github' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'View' }))

    await waitFor(() => expect(getSpy).toHaveBeenCalledWith('a1'))
    // The masked header and the pretty-printed JSON body render in the detail modal.
    expect(await screen.findByText('***masked***')).toBeInTheDocument()
    expect(screen.getByText(/"ok": true/)).toBeInTheDocument()
  })
})
