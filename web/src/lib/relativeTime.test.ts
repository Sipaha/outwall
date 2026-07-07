import { describe, it, expect } from 'vitest'
import { relTime, absTime } from './relativeTime'

const ago = (ms: number) => new Date(Date.now() - ms).toISOString()
const S = 1000
const M = 60 * S
const H = 60 * M
const D = 24 * H

describe('relTime', () => {
  it('renders recent times relatively', () => {
    expect(relTime(ago(5 * S))).toBe('just now')
    expect(relTime(ago(4 * M))).toBe('4m ago')
    expect(relTime(ago(2 * H))).toBe('2h ago')
    expect(relTime(ago(3 * D))).toBe('3d ago')
  })
  it('treats small future skew as just now', () => {
    expect(relTime(new Date(Date.now() + 3 * S).toISOString())).toBe('just now')
  })
  it('falls back to an absolute date past a week', () => {
    const old = ago(10 * D)
    expect(relTime(old)).toBe(new Date(old).toLocaleDateString())
  })
  it('returns the raw string when it does not parse', () => {
    expect(relTime('not-a-date')).toBe('not-a-date')
  })
})

describe('absTime', () => {
  it('renders the full localized date+time', () => {
    const iso = '2026-06-17T10:00:00Z'
    expect(absTime(iso)).toBe(new Date(iso).toLocaleString())
  })
})
