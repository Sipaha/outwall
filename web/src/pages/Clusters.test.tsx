import { afterEach, describe, expect, it, vi } from 'vitest'
import { createRef } from 'react'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'
import { Clusters, type ClustersHandle } from './Clusters'
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
      // A cluster whose k8s credential was lost (empty k8s_auth) → flagged as misconfigured.
      { id: 'c3', name: 'broken', base_url: 'https://broken:6443', auth_type: 'static', kind: 'k8s', k8s_auth: '' },
    ])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    render(<Clusters />)
    expect(await screen.findByText('prod')).toBeInTheDocument()
    expect(screen.getByText('lab')).toBeInTheDocument()
    // the http upstream must NOT appear in the Clusters list
    expect(screen.queryByText('github')).not.toBeInTheDocument()
    // the insecure cluster shows a red "insecure" badge
    expect(screen.getByText('insecure')).toBeInTheDocument()
    // a cluster with no k8s auth is flagged, not shown as a blank badge
    expect(screen.getByText(/no auth/i)).toBeInTheDocument()
  })

  it('picks a kubeconfig file, uploads its content, and toasts the result', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const importSpy = vi
      .spyOn(api, 'importKubeconfigContent')
      .mockResolvedValue({ added: ['prod', 'staging'], updated: [], skipped: ['lab'] })
    render(
      <>
        <Clusters />
        <ToastContainer />
      </>,
    )

    // The "Import from kubeconfig" button triggers a hidden file <input>; selecting a file reads
    // its text and posts the content.
    const input = screen.getByLabelText('Import kubeconfig file') as HTMLInputElement
    const file = new File(['apiVersion: v1\nkind: Config\n'], 'config.yaml', { type: 'text/yaml' })
    fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(importSpy).toHaveBeenCalled())
    expect(importSpy.mock.calls[0][0]).toContain('apiVersion: v1')
    expect(await screen.findByText(/added 2/i)).toBeInTheDocument()
  })

  it('shows a success toast (not a false error) when the upload returns added:null', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    // The Go daemon used to encode an all-skipped import's nil slice as JSON null; the UI must
    // null-guard so an HTTP-200 import never fires "Failed to import clusters".
    vi.spyOn(api, 'importKubeconfigContent').mockResolvedValue({
      added: null as unknown as string[],
      updated: null as unknown as string[],
      skipped: ['lab'],
    })
    render(
      <>
        <Clusters />
        <ToastContainer />
      </>,
    )

    const input = screen.getByLabelText('Import kubeconfig file') as HTMLInputElement
    const file = new File(['apiVersion: v1\n'], 'config.yaml', { type: 'text/yaml' })
    fireEvent.change(input, { target: { files: [file] } })

    expect(await screen.findByText(/added 0, updated 0, skipped 1/i)).toBeInTheDocument()
    expect(screen.queryByText(/Failed to import/i)).not.toBeInTheDocument()
  })

  it('shows exec fields when the add-cluster auth type is exec', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const ref = createRef<ClustersHandle>()
    render(<Clusters ref={ref} />)
    act(() => ref.current!.openAdd())

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
