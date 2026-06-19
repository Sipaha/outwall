import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Settings } from './Settings'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Settings> audit retention', () => {
  it('loads the current retention and saves a new value', async () => {
    vi.spyOn(api, 'getVaultStatus').mockResolvedValue({ initialized: true, locked: false })
    vi.spyOn(api, 'getAuditRetention').mockResolvedValue({ days: 14 })
    const setSpy = vi.spyOn(api, 'setAuditRetention').mockResolvedValue({ days: 30 })

    render(<Settings />)

    const input = (await screen.findByLabelText('Retention days')) as HTMLInputElement
    await waitFor(() => expect(input.value).toBe('14'))

    fireEvent.change(input, { target: { value: '30' } })
    fireEvent.click(screen.getByRole('button', { name: /Save auto-prune/i }))

    await waitFor(() => expect(setSpy).toHaveBeenCalledWith(30))
  })
})
