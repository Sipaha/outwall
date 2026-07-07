import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { DurationSelect, DURATION_OPTIONS, DEFAULT_TTL_SECONDS } from './DurationSelect'

describe('DurationSelect', () => {
  it('lists every option incl. Бессрочно and defaults to 1h', () => {
    expect(DEFAULT_TTL_SECONDS).toBe(3600)
    expect(DURATION_OPTIONS.map((o) => o.seconds)).toEqual([3600, 7200, 28800, 86400, 172800, 604800, 2592000, 31536000, 0])
    render(<DurationSelect value={3600} onChange={() => {}} />)
    expect(screen.getByRole('option', { name: 'Бессрочно' })).toBeInTheDocument()
  })
  it('emits the chosen seconds as a number', () => {
    const onChange = vi.fn()
    render(<DurationSelect value={3600} onChange={onChange} />)
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '86400' } })
    expect(onChange).toHaveBeenCalledWith(86400)
  })
})
