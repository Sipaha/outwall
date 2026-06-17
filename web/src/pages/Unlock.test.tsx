import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Unlock } from './Unlock'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Unlock>', () => {
  it('calls vaultUnlock with the typed password and onDone on success', async () => {
    const unlockSpy = vi.spyOn(api, 'vaultUnlock').mockResolvedValue({ locked: false })
    const onDone = vi.fn()
    render(<Unlock mode="unlock" onDone={onDone} />)

    fireEvent.change(screen.getByLabelText('Master password'), { target: { value: 's3cret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Unlock' }))

    await waitFor(() => expect(unlockSpy).toHaveBeenCalledWith('s3cret'))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
  })

  it('shows the daemon error and does not call onDone on a bad password', async () => {
    vi.spyOn(api, 'vaultUnlock').mockRejectedValue(new api.ApiError('incorrect master password', 401))
    const onDone = vi.fn()
    render(<Unlock mode="unlock" onDone={onDone} />)

    fireEvent.change(screen.getByLabelText('Master password'), { target: { value: 'nope' } })
    fireEvent.click(screen.getByRole('button', { name: 'Unlock' }))

    expect(await screen.findByText('incorrect master password')).toBeInTheDocument()
    expect(onDone).not.toHaveBeenCalled()
  })

  it('init mode requires matching confirmation before calling vaultInit', async () => {
    const initSpy = vi.spyOn(api, 'vaultInit').mockResolvedValue({ initialized: true })
    render(<Unlock mode="init" onDone={vi.fn()} />)

    fireEvent.change(screen.getByLabelText('Master password'), { target: { value: 'a' } })
    fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'b' } })
    fireEvent.click(screen.getByRole('button', { name: 'Set password' }))

    expect(await screen.findByText('Passwords do not match')).toBeInTheDocument()
    expect(initSpy).not.toHaveBeenCalled()
  })
})
