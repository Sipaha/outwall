import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { ScopeBadge } from './scope'

afterEach(cleanup)

describe('<ScopeBadge>', () => {
  it('renders the label with a write-danger class', () => {
    render(<ScopeBadge scope={{ label: 'WRITE', kind: 'write' }} />)
    const el = screen.getByText('WRITE')
    expect(el.className).toContain('text-warning')
  })
  it('renders a method scope', () => {
    render(<ScopeBadge scope={{ label: 'GET', kind: 'method' }} />)
    expect(screen.getByText('GET')).toBeInTheDocument()
  })
})
