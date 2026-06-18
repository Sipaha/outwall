import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Approvals } from './Approvals'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Approvals>', () => {
  it('approves a pending approval via resolveApproval(id, true)', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'p1',
        agent_id: 'agent-1234',
        upstream_id: 'up-1234',
        method: 'GET',
        path: '/repos',
        purpose: 'fetch repos',
        created_at: '2026-06-17T10:00:00Z',
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
    const resolveSpy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })

    render(<Approvals />)

    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }))

    await waitFor(() => expect(resolveSpy).toHaveBeenCalledWith('p1', true))
  })

  it('renders the k8s tuple and the patch body for a k8s approval', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'p2',
        agent_id: 'agent-9999',
        upstream_id: 'up-9999',
        method: 'PATCH',
        path: '/api/v1/namespaces/prod/deployments/web',
        purpose: 'bump image',
        created_at: '2026-06-18T10:00:00Z',
        namespace: 'prod',
        resource: 'deployments',
        verb: 'patch',
        request_body: '{"spec":{"image":"web:v2"}}',
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])

    render(<Approvals />)

    // The parsed tuple is shown.
    expect(await screen.findByText('prod')).toBeInTheDocument()
    expect(screen.getByText('deployments')).toBeInTheDocument()
    expect(screen.getByText('patch')).toBeInTheDocument()
    // The patch body (the change) is rendered.
    expect(screen.getByText(/"image":"web:v2"/)).toBeInTheDocument()
  })

  // --- H3: MCP control-plane host card ---

  it('renders a host-access card and approves with an attached credential', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'h1',
        agent_id: 'agent-claude',
        upstream_id: 'up-host',
        method: '',
        path: '',
        purpose: 'check CI state',
        created_at: '2026-06-18T10:00:00Z',
        kind: 'host-access',
        host: 'api.example.test',
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
    const resolveSpy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })

    render(<Approvals />)

    // host + purpose are shown.
    expect(await screen.findByText('api.example.test')).toBeInTheDocument()
    expect(screen.getByText('check CI state')).toBeInTheDocument()

    // Enter a credential then approve.
    fireEvent.change(screen.getByLabelText('Header'), { target: { value: 'Authorization' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: 'Bearer xyz' } })
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))

    await waitFor(() =>
      expect(resolveSpy).toHaveBeenCalledWith('h1', true, {
        auth: { type: 'static', header: 'Authorization', token: 'Bearer xyz' },
      }),
    )
  })

  // --- H3: MCP control-plane operation card ---

  it('renders an operation card with the example URL, trust-any checkboxes and a broad-placeholder warning', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'o1',
        agent_id: 'agent-claude',
        upstream_id: 'up-host',
        method: '',
        path: '',
        purpose: 'check CI state',
        created_at: '2026-06-18T10:00:00Z',
        kind: 'operation',
        host: 'api.example.test',
        op_method: 'GET',
        op_path_template: '/api/v4/projects/{project_path:text}/pipelines',
        op_query_template: { updated_after: '{since:date}' },
        op_variables: [
          { name: 'project_path', type: 'text' },
          { name: 'since', type: 'date' },
        ],
        op_values: { project_path: 'infra/helm', since: '2026-01-01' },
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
    const resolveSpy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })

    render(<Approvals />)

    // The concrete example URL built from the requested values appears.
    expect(
      await screen.findByText(
        'GET https://api.example.test/api/v4/projects/infra/helm/pipelines?updated_after=2026-01-01',
      ),
    ).toBeInTheDocument()

    // A per-text-variable trust-any checkbox exists (date vars get none).
    const trustAny = screen.getByLabelText('Trust any value for project_path')
    expect(trustAny).toBeInTheDocument()
    expect(screen.queryByLabelText('Trust any value for since')).not.toBeInTheDocument()

    // No warning yet (no broad var).
    expect(screen.queryByText(/grants access to ANY value/i)).not.toBeInTheDocument()

    // Toggling trust-any surfaces the broad-placeholder warning.
    fireEvent.click(trustAny)
    expect(screen.getByText(/grants access to ANY value/i)).toBeInTheDocument()

    // Approve posts the chosen trust_any vars.
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() =>
      expect(resolveSpy).toHaveBeenCalledWith('o1', true, { trust_any: ['project_path'] }),
    )
  })

  // --- H3: data-plane new-value card ---

  it('renders a new-value card and approves with trust-any', async () => {
    vi.spyOn(api, 'listApprovals').mockResolvedValue([
      {
        id: 'n1',
        agent_id: 'agent-claude',
        upstream_id: 'up-host',
        method: 'GET',
        path: '/api/v4/projects/other/pipelines',
        purpose: '',
        created_at: '2026-06-18T10:00:00Z',
        template: '/api/v4/projects/{project_path:text}/pipelines',
        rule_id: 'rule-1',
        new_values: [{ var: 'project_path', value: 'other' }],
      },
    ])
    vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
    const resolveSpy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })

    render(<Approvals />)

    // Template + the new (variable, value) are shown.
    expect(
      await screen.findByText('/api/v4/projects/{project_path:text}/pipelines'),
    ).toBeInTheDocument()
    expect(screen.getByText('project_path')).toBeInTheDocument()
    expect(screen.getByText('other')).toBeInTheDocument()

    // "Approve + trust any" posts trust_any for the variable.
    fireEvent.click(screen.getByRole('button', { name: 'Approve + trust any' }))
    await waitFor(() =>
      expect(resolveSpy).toHaveBeenCalledWith('n1', true, { trust_any: ['project_path'] }),
    )
  })
})
