import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { RuleRow } from './RuleRow'
import * as api from '../../lib/api'
import type { Rule } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const rule: Rule = {
  id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET',
  op_path_template: '/api/v4/projects/{project_path:text}/pipelines',
  op_value_policies: { project_path: { type: 'text', mode: 'set', values: ['infra/helm'] } },
  outcome: 'allow', rate_limit_per_min: 0,
}

describe('<RuleRow>', () => {
  it('shows scope, path and the value summary tail', () => {
    render(<RuleRow rule={rule} onChanged={() => {}} />)
    expect(screen.getByText('GET')).toBeInTheDocument()
    expect(screen.getByText(/project_path: infra\/helm/)).toBeInTheDocument()
  })
  it('expands to show the text value-set editor with the chip', () => {
    render(<RuleRow rule={rule} onChanged={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /expand rule/i }))
    expect(screen.getByText('infra/helm')).toBeInTheDocument()
    expect(screen.getByLabelText('Trust any value for project_path')).toBeInTheDocument()
  })
  it('deletes the rule', async () => {
    const spy = vi.spyOn(api, 'deleteRule').mockResolvedValue({ ok: true })
    const onChanged = vi.fn()
    render(<RuleRow rule={rule} onChanged={onChanged} />)
    fireEvent.click(screen.getByRole('button', { name: /delete rule/i }))
    await waitFor(() => expect(spy).toHaveBeenCalledWith('r1'))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
