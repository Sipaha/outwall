import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
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
})
