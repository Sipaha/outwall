import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { RequestsPanel } from './RequestsPanel'
import * as api from '../../lib/api'
import type { Approval } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const hostApproval: Approval = {
  id: 'a1', agent_id: 'ag1', upstream_id: 'up1', method: '', path: '', purpose: 'read logs',
  created_at: '2026-07-07T10:00:00Z', kind: 'host-access', host: 'gitlab.example.com',
}

describe('<RequestsPanel>', () => {
  it('shows the count and renders an approval card', () => {
    render(<RequestsPanel approvals={[hostApproval]} onChanged={() => {}} />)
    expect(screen.getByText('Запросы прав')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
    expect(screen.getByText('gitlab.example.com')).toBeInTheDocument()
  })
  it('shows an empty state when there are none', () => {
    render(<RequestsPanel approvals={[]} onChanged={() => {}} />)
    expect(screen.getByText('Нет запросов прав')).toBeInTheDocument()
  })
  it('approves via resolveApproval and calls onChanged', async () => {
    const spy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })
    const onChanged = vi.fn()
    render(<RequestsPanel approvals={[hostApproval]} onChanged={onChanged} />)
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(spy).toHaveBeenCalled())
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
