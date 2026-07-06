import { useEffect, useRef } from 'react'

/**
 * Routes to `/approvals` when a NEW desktop "open-approvals" signal arrives — i.e. when the monotonic
 * `desktop.open-approvals` counter INCREASES (each OS-notification click bumps it; see
 * cmd/outwall-desktop notifs.OnNotificationResponse). It must gate on the increase, not on `> 0`:
 * the counter never resets, and react-router v7 hands out a fresh `navigate` identity on every
 * navigation, so a `> 0` guard re-fired this effect on each render and forced the operator back to
 * Approvals on every tab switch (a navigation trap). Tracking the last-handled count in a ref makes
 * the navigation fire exactly once per signal.
 */
export function useOpenApprovalsRoute(openApprovals: number, navigate: (to: string) => void): void {
  const lastHandled = useRef(0)
  useEffect(() => {
    if (openApprovals > lastHandled.current) {
      lastHandled.current = openApprovals
      navigate('/approvals')
    }
  }, [openApprovals, navigate])
}
