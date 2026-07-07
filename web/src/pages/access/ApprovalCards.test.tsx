import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { ApprovalCard } from './ApprovalCards'
import * as api from '../../lib/api'
import type { Approval } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

function presetApproval(presetId: string, label: string): Approval {
  return {
    id: 'ap1', agent_id: 'ag1', upstream_id: 'up1', method: '', path: '', purpose: 'p',
    created_at: '2026-07-07T10:00:00Z', kind: 'preset', host: 'unilever-finance.ecos24.ru',
    preset_id: presetId, bindings: {}, preset: { id: presetId, label, slots: [] },
  }
}

function operationApproval(): Approval {
  return {
    id: 'ap2', agent_id: 'ag1', upstream_id: 'up1', method: '', path: '', purpose: 'p',
    created_at: '2026-07-07T10:00:00Z', kind: 'operation', host: 'gitlab.example.com',
    op_method: 'GET', op_path_template: '/projects/{project_path:text}/pipelines',
    op_variables: [{ name: 'project_path', type: 'text' }],
  }
}

describe('<PresetCard> scope badge (derived from the live preview, not the id)', () => {
  it('a browse-get preset (GET/HEAD only) reads READ, never READ/WRITE', async () => {
    vi.spyOn(api, 'previewPreset').mockResolvedValue({ rules: ['allow browse GET,HEAD /**'] })
    render(<ApprovalCard approval={presetApproval('browse-get', 'Browse (GET)')} onResolve={() => {}} />)
    await waitFor(() => expect(screen.getByText('READ')).toBeInTheDocument())
    expect(screen.queryByText('READ/WRITE')).not.toBeInTheDocument()
  })

  it('a preset that creates a citeck write rule reads READ/WRITE', async () => {
    vi.spyOn(api, 'previewPreset').mockResolvedValue({
      rules: ['allow browse GET,HEAD /**', 'allow citeck {"op":"read"}', 'allow citeck {"op":"write"}'],
    })
    render(<ApprovalCard approval={presetApproval('citeck-readwrite', 'ReadWrite')} onResolve={() => {}} />)
    await waitFor(() => expect(screen.getByText('READ/WRITE')).toBeInTheDocument())
  })

  it('an unrecognised (not provably read-only) rule shape is neutral, never READ', async () => {
    // browse with ANY method ("*") is not provably GET/HEAD-only → must not claim READ.
    vi.spyOn(api, 'previewPreset').mockResolvedValue({ rules: ['allow browse * /**'] })
    render(<ApprovalCard approval={presetApproval('some-preset', 'Some')} onResolve={() => {}} />)
    await waitFor(() => expect(screen.getByText('СМ. НИЖЕ')).toBeInTheDocument())
    expect(screen.queryByText('READ')).not.toBeInTheDocument()
    expect(screen.queryByText('READ/WRITE')).not.toBeInTheDocument()
  })
})

describe('grant-duration dropdown on rule-creating cards', () => {
  it('passes the chosen ttl_seconds when approving a preset', async () => {
    vi.spyOn(api, 'previewPreset').mockResolvedValue({ rules: ['allow browse GET,HEAD /**'] })
    const onResolve = vi.fn()
    render(<ApprovalCard approval={presetApproval('browse-get', 'Browse (GET)')} onResolve={onResolve} />)
    await screen.findByText('READ')
    fireEvent.change(screen.getByRole('combobox', { name: 'grant duration' }), { target: { value: '28800' } })
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onResolve).toHaveBeenCalledWith('ap1', true, expect.objectContaining({ ttl_seconds: 28800 }))
  })

  it('passes the chosen ttl_seconds when approving an operation', () => {
    const onResolve = vi.fn()
    render(<ApprovalCard approval={operationApproval()} onResolve={onResolve} />)
    fireEvent.change(screen.getByRole('combobox', { name: 'grant duration' }), { target: { value: '604800' } })
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onResolve).toHaveBeenCalledWith('ap2', true, expect.objectContaining({ ttl_seconds: 604800 }))
  })
})
