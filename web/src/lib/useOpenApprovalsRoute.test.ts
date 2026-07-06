import { describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useOpenApprovalsRoute } from './useOpenApprovalsRoute'

describe('useOpenApprovalsRoute', () => {
  it('navigates to /approvals only when the signal counter INCREASES, not on re-render', () => {
    const navigate = vi.fn()
    const { rerender } = renderHook(({ n, nav }) => useOpenApprovalsRoute(n, nav), {
      initialProps: { n: 0, nav: navigate },
    })
    expect(navigate).not.toHaveBeenCalled() // counter 0 → nothing to route to

    rerender({ n: 1, nav: navigate }) // first "open-approvals" signal
    expect(navigate).toHaveBeenCalledTimes(1)
    expect(navigate).toHaveBeenCalledWith('/approvals')

    // The bug: react-router v7 hands out a NEW `navigate` identity on each navigation, which re-fires
    // the effect. With the same counter it must NOT navigate again (otherwise the operator is trapped
    // on Approvals — every tab switch bounces back).
    const navigate2 = vi.fn()
    rerender({ n: 1, nav: navigate2 })
    expect(navigate2).not.toHaveBeenCalled()

    // A genuinely new signal routes again (the feature is preserved).
    rerender({ n: 2, nav: navigate2 })
    expect(navigate2).toHaveBeenCalledTimes(1)
    expect(navigate2).toHaveBeenCalledWith('/approvals')
  })
})
