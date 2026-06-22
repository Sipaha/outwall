import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'
import { Rules } from './Rules'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const githubUpstream = {
  id: 'u1',
  name: 'github',
  base_url: 'https://api.github.com',
  auth_type: 'none',
}

function operationRule() {
  return {
    id: 'r1',
    subject_agent_id: '',
    upstream_id: 'u1',
    op_method: 'GET',
    op_path_template: '/api/v4/projects/{project_path:text}/pipelines',
    op_query_template: { updated_after: '{since:date}' },
    op_value_policies: {
      project_path: { type: 'text', mode: 'set', values: ['infra/helm'] },
      since: { type: 'date', mode: 'any' },
    },
    outcome: 'allow',
    rate_limit_per_min: 0,
  }
}

describe('<Operations> (Rules.tsx)', () => {
  it('renders an operation template with its per-variable value-set', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([operationRule()])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([githubUpstream])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])

    render(<Rules />)

    // The host and the template are shown (the template renders fixed vs variable segments, so the
    // variable placeholder and a fixed piece each appear as their own node).
    expect(await screen.findByText('github')).toBeInTheDocument()
    expect(screen.getByText('{project_path:text}')).toBeInTheDocument()
    expect(screen.getByText(/\/api\/v4\/projects\//)).toBeInTheDocument()
    // The text variable's allowed value is a chip; the date var is shown as auto.
    expect(screen.getByText('infra/helm')).toBeInTheDocument()
    expect(screen.getByText(/auto/i)).toBeInTheDocument()
  })

  it('edits number (range) and enum (closed-set) variables', async () => {
    const rule = {
      id: 'r2',
      subject_agent_id: '',
      upstream_id: 'u1',
      op_method: 'GET',
      op_path_template: '/items/{id:number}',
      op_query_template: { sort: '{order:enum}' },
      op_body_template: { name: '{label:text}' },
      op_value_policies: {
        id: { type: 'number', mode: 'range', min: 1, max: 100 },
        order: { type: 'enum', mode: 'set', values: ['asc'] },
      },
      outcome: 'allow',
      rate_limit_per_min: 0,
    }
    vi.spyOn(api, 'listRules').mockResolvedValue([rule])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([githubUpstream])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const setSpy = vi.spyOn(api, 'setRuleVariablePolicy').mockResolvedValue({ ok: true })

    render(<Rules />)
    await screen.findByText('github')

    // the body template is shown
    expect(screen.getByText(/body:/)).toBeInTheDocument()
    expect(screen.getByText(/Closed domain/i)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Value to add for order'), { target: { value: 'desc' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add value for order' }))
    await waitFor(() =>
      expect(setSpy).toHaveBeenCalledWith('r2', 'order', {
        type: 'enum',
        mode: 'set',
        values: ['asc', 'desc'],
      }),
    )

    fireEvent.blur(screen.getByLabelText('Maximum for id'), { target: { value: '50' } })
    await waitFor(() =>
      expect(setSpy).toHaveBeenCalledWith('r2', 'id', {
        type: 'number',
        mode: 'range',
        min: 1,
        max: 50,
      }),
    )
  })

  it('adds a value to a text variable (posts the grown set)', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([operationRule()])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([githubUpstream])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const setSpy = vi.spyOn(api, 'setRuleVariablePolicy').mockResolvedValue({ ok: true })

    render(<Rules />)
    await screen.findByText('infra/helm')

    fireEvent.change(screen.getByLabelText('Value to add for project_path'), {
      target: { value: 'infra/charts' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Add value for project_path' }))

    await waitFor(() =>
      expect(setSpy).toHaveBeenCalledWith('r1', 'project_path', {
        type: 'text',
        mode: 'set',
        values: ['infra/helm', 'infra/charts'],
      }),
    )
  })

  it('removes a value from a text variable (posts the trimmed set)', async () => {
    const rule = operationRule()
    rule.op_value_policies.project_path.values = ['infra/helm', 'infra/charts']
    vi.spyOn(api, 'listRules').mockResolvedValue([rule])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([githubUpstream])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const setSpy = vi.spyOn(api, 'setRuleVariablePolicy').mockResolvedValue({ ok: true })

    render(<Rules />)
    await screen.findByText('infra/helm')

    fireEvent.click(screen.getByRole('button', { name: 'Remove infra/helm from project_path' }))

    await waitFor(() =>
      expect(setSpy).toHaveBeenCalledWith('r1', 'project_path', {
        type: 'text',
        mode: 'set',
        values: ['infra/charts'],
      }),
    )
  })

  it('toggles a text variable to "any" (posts mode any)', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([operationRule()])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([githubUpstream])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const setSpy = vi.spyOn(api, 'setRuleVariablePolicy').mockResolvedValue({ ok: true })

    render(<Rules />)
    await screen.findByText('infra/helm')

    fireEvent.click(screen.getByLabelText('Trust any value for project_path'))

    await waitFor(() =>
      expect(setSpy).toHaveBeenCalledWith('r1', 'project_path', { type: 'text', mode: 'any' }),
    )
  })

  it('submits createRule from the add-operation modal', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([githubUpstream])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'new' })

    render(<Rules />)
    await screen.findByText('No operations yet — default-deny applies')

    fireEvent.click(screen.getByRole('button', { name: 'Add operation' }))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith({
        subject_agent_id: '',
        upstream_id: 'u1',
        op_method: 'GET',
        op_path_template: '',
        op_value_policies: {},
        outcome: 'allow',
        rate_limit_per_min: 0,
      }),
    )
  })

  it('shows k8s namespace/resource/verb fields when the selected upstream is a cluster', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'c1', name: 'prod-cluster', base_url: 'https://k8s', auth_type: 'none', kind: 'k8s' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'new' })

    render(<Rules />)
    await screen.findByText('No operations yet — default-deny applies')

    fireEvent.click(screen.getByRole('button', { name: 'Add operation' }))

    const ns = await screen.findByLabelText('Namespace')
    fireEvent.change(ns, { target: { value: 'prod' } })
    fireEvent.change(screen.getByLabelText('Resource'), { target: { value: 'deployments' } })
    fireEvent.change(screen.getByLabelText('Verb'), { target: { value: 'patch' } })
    expect(screen.queryByLabelText('Operation path-template')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          upstream_id: 'c1',
          namespace: 'prod',
          resource: 'deployments',
          verb: 'patch',
          outcome: 'allow',
        }),
      ),
    )
  })

  it('creates a citeck Records rule', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'up', name: 'c.test', base_url: 'https://c.test', auth_type: 'none', profile: 'citeck' },
    ])
    vi.spyOn(api, 'listRules').mockResolvedValue([])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'r1' })
    render(<Rules />)

    await screen.findByText('No operations yet — default-deny applies')

    fireEvent.click(screen.getByRole('button', { name: 'Add operation' }))
    // Host selector is pre-populated with the only upstream; citeck fields should appear
    await screen.findByLabelText('Records operation')
    fireEvent.change(screen.getByLabelText('Records operation'), { target: { value: 'read' } })
    fireEvent.change(screen.getByLabelText('Source ID'), { target: { value: 'emodel/type' } })
    fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'w1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          upstream_id: 'up',
          outcome: 'allow',
          profile: 'citeck',
          profile_params: { op: 'read', source_id: 'emodel/type', workspace: 'w1' },
        }),
      ),
    )
  })

  it('shows a server-profile rule in the Server-profile rules section (not invisible)', async () => {
    const profileRule = {
      id: 'r1',
      subject_agent_id: '',
      upstream_id: 'up',
      outcome: 'allow' as const,
      rate_limit_per_min: 0,
      profile: 'citeck',
      profile_params: { op: 'read', source_id: 'emodel/type', workspace: 'w1' },
    }
    vi.spyOn(api, 'listRules').mockResolvedValue([profileRule])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'up', name: 'c.test', base_url: 'https://c.test', auth_type: 'none', profile: 'citeck' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const deleteSpy = vi.spyOn(api, 'deleteRule').mockResolvedValue({ ok: true })

    render(<Rules />)

    // The profile rule must be visible: source_id value appears in the Server-profile rules section.
    const section = (await screen.findByText('Server-profile rules')).closest('section')!
    expect(within(section).getByText(/emodel\/type/)).toBeInTheDocument()
    // A Delete button must be present for it.
    const deleteBtn = within(section).getByRole('button', { name: /delete/i })
    expect(deleteBtn).toBeInTheDocument()
    // Clicking Delete opens the confirm modal; the confirm modal contains the confirmation text.
    fireEvent.click(deleteBtn)
    // Wait for the confirm text to appear, then click the destructive Delete button in the modal footer.
    await screen.findByText(/delete this operation\?/i)
    // There are two Delete buttons at this point: the row one and the modal confirm one.
    // The modal confirm button has bg-destructive class (not bg-destructive/15).
    const confirmBtn = screen.getAllByRole('button', { name: /^delete$/i }).find(
      (b) => b.className.includes('bg-destructive ') || b.className.includes('bg-destructive\n'),
    )
    expect(confirmBtn).toBeDefined()
    fireEvent.click(confirmBtn!)
    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith('r1'))
  })

  it('keeps k8s rules in a separate tuple list (not the operations editor)', async () => {
    vi.spyOn(api, 'listRules').mockResolvedValue([
      {
        id: 'k1',
        subject_agent_id: '',
        upstream_id: 'c1',
        outcome: 'allow',
        rate_limit_per_min: 0,
        namespace: 'prod',
        resource: 'deployments',
        verb: 'patch',
      },
    ])
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'c1', name: 'prod-cluster', base_url: 'https://k8s', auth_type: 'none', kind: 'k8s' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])

    render(<Rules />)

    const k8sSection = (await screen.findByText('Cluster (k8s) rules')).closest('section')!
    expect(within(k8sSection).getByText('prod-cluster')).toBeInTheDocument()
    expect(within(k8sSection).getByText('patch')).toBeInTheDocument()
  })
})
