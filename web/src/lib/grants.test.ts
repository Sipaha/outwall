import { describe, it, expect } from 'vitest'
import { deriveGrants, scopeOf, valueSummary } from './grants'
import type { Rule, AccessRequest } from './types'

const httpRule: Rule = {
  id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1',
  op_method: 'GET', op_path_template: '/api/v4/projects/{project_path:text}/pipelines',
  op_value_policies: {
    project_path: { type: 'text', mode: 'set', values: ['infra/helm', 'infra/charts'] },
    page: { type: 'number', mode: 'range', min: 1, max: 50 },
  },
  outcome: 'allow', rate_limit_per_min: 0,
}
const profRule: Rule = {
  id: 'r2', subject_agent_id: 'ag1', upstream_id: 'up2',
  profile: 'citeck', profile_params: { op: 'write' }, outcome: 'allow', rate_limit_per_min: 0,
}
const k8sRule: Rule = {
  id: 'r3', subject_agent_id: 'ag1', upstream_id: 'up3',
  namespace: 'prod', resource: 'pods', verb: 'get', outcome: 'allow', rate_limit_per_min: 0,
}

describe('scopeOf', () => {
  it('http → method', () => expect(scopeOf(httpRule)).toEqual({ label: 'GET', kind: 'method' }))
  it('citeck write → WRITE', () => expect(scopeOf(profRule)).toEqual({ label: 'WRITE', kind: 'write' }))
  it('k8s → verb', () => expect(scopeOf(k8sRule)).toEqual({ label: 'get', kind: 'verb' }))
})

describe('valueSummary', () => {
  it('lists non-any values and ranges', () => {
    expect(valueSummary(httpRule)).toBe('project_path: infra/helm, infra/charts · page: 1–50')
  })
  it('empty when no policies', () => expect(valueSummary(profRule)).toBe(''))
})

describe('deriveGrants', () => {
  it('groups rules by (agent, upstream) and attaches the granted request purpose', () => {
    const reqs: AccessRequest[] = [{
      id: 'req1', agent_id: 'ag1', agent_name: 'claude', upstream_id: 'up1', upstream_name: 'gitlab',
      purpose: 'CI monitoring', status: 'granted', created_at: '2026-06-17T10:00:00Z',
      resolved_at: '2026-06-17T10:05:00Z',
    }]
    const grants = deriveGrants([httpRule, profRule], reqs)
    expect(grants).toHaveLength(2)
    const g1 = grants.find((g) => g.upstreamId === 'up1')!
    expect(g1.rules).toHaveLength(1)
    expect(g1.purpose).toBe('CI monitoring')
    expect(g1.grantedAt).toBe('2026-06-17T10:05:00Z')
  })
})
