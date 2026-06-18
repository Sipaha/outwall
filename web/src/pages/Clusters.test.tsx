import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { Clusters } from './Clusters'
import { Upstreams } from './Upstreams'
import { ToastContainer } from '../components/Toast'
import * as api from '../lib/api'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('<Clusters>', () => {
  it('lists kind=k8s clusters and shows an insecure badge', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'c1', name: 'prod', base_url: 'https://prod:6443', auth_type: 'none', kind: 'k8s', k8s_auth: 'token' },
      {
        id: 'c2',
        name: 'lab',
        base_url: 'https://lab:6443',
        auth_type: 'none',
        kind: 'k8s',
        k8s_auth: 'token',
        k8s_insecure: true,
      },
      { id: 'u1', name: 'github', base_url: 'https://api.github.com', auth_type: 'static', kind: 'http' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    render(<Clusters />)
    expect(await screen.findByText('prod')).toBeInTheDocument()
    expect(screen.getByText('lab')).toBeInTheDocument()
    // the http upstream must NOT appear in the Clusters list
    expect(screen.queryByText('github')).not.toBeInTheDocument()
    // the insecure cluster shows a red "insecure" badge
    expect(screen.getByText('insecure')).toBeInTheDocument()
  })

  it('imports from kubeconfig and toasts the result', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const importSpy = vi
      .spyOn(api, 'importClusters')
      .mockResolvedValue({ added: ['prod', 'staging'], skipped: ['lab'] })
    render(
      <>
        <Clusters />
        <ToastContainer />
      </>,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Import from kubeconfig' }))
    await waitFor(() => expect(importSpy).toHaveBeenCalled())
    // a toast summarizing added/skipped appears
    expect(await screen.findByText(/added 2/i)).toBeInTheDocument()
  })

  it('shows exec fields when the add-cluster auth type is exec', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    render(<Clusters />)
    fireEvent.click(screen.getByRole('button', { name: 'Add cluster' }))

    // token is the default → no exec command field yet
    expect(screen.queryByLabelText('Command')).not.toBeInTheDocument()

    fireEvent.change(screen.getByDisplayValue('Token'), { target: { value: 'exec' } })
    expect(await screen.findByLabelText('Command')).toBeInTheDocument()
  })
})

describe('<Upstreams> filters out k8s', () => {
  it('does not list a kind=k8s cluster', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([
      { id: 'u1', name: 'github', base_url: 'https://api.github.com', auth_type: 'static', kind: 'http' },
      { id: 'c1', name: 'prod-cluster', base_url: 'https://prod:6443', auth_type: 'none', kind: 'k8s' },
    ])
    render(<Upstreams />)
    expect(await screen.findByText('github')).toBeInTheDocument()
    expect(screen.queryByText('prod-cluster')).not.toBeInTheDocument()
  })
})
