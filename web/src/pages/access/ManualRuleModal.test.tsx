import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { ManualRuleModal } from './ManualRuleModal'
import * as api from '../../lib/api'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('<ManualRuleModal>', () => {
  it('creates an http operation rule', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([{ id: 'up1', name: 'gitlab', base_url: '', auth_type: '', kind: 'http' }])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const spy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'r1' })
    const onCreated = vi.fn()
    render(<ManualRuleModal open onClose={() => {}} onCreated={onCreated} />)
    await screen.findByLabelText('Operation path-template')
    fireEvent.change(screen.getByLabelText('Operation path-template'), { target: { value: '/x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(spy).toHaveBeenCalled())
    await waitFor(() => expect(onCreated).toHaveBeenCalled())
  })
})
