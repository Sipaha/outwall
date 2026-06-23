import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import {
  listUpstreams,
  listAgents,
  createCluster,
  deleteUpstream,
  importKubeconfigContent,
  getKubeconfig,
  ApiError,
} from '../lib/api'
import type { Upstream, Agent, ClusterAuthConfig } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { Modal } from '../components/Modal'
import { FormField, fieldControlClass } from '../components/FormField'
import { Select } from '../components/Select'
import { useToastStore } from '../lib/toast'

type ClusterAuthType = 'token' | 'client-cert' | 'exec'

interface ClusterDraft {
  name: string
  baseURL: string
  ca: string
  authType: ClusterAuthType
  token: string
  clientCert: string
  clientKey: string
  execCommand: string
  execArgs: string
}

const emptyDraft: ClusterDraft = {
  name: '',
  baseURL: '',
  ca: '',
  authType: 'token',
  token: '',
  clientCert: '',
  clientKey: '',
  execCommand: '',
  execArgs: '',
}

export type ClustersHandle = { openImport: () => void; openAdd: () => void }

export const Clusters = forwardRef<ClustersHandle, { onImportingChange?: (importing: boolean) => void }>(
  function Clusters({ onImportingChange }, ref) {
    const [clusters, setClusters] = useState<Upstream[]>([])
    const [agents, setAgents] = useState<Agent[]>([])
    const [open, setOpen] = useState(false)
    const [draft, setDraft] = useState<ClusterDraft>(emptyDraft)
    const [busy, setBusy] = useState(false)
    const [confirmDelete, setConfirmDelete] = useState<Upstream | null>(null)
    // Kubeconfig flow: pick a cluster, choose an agent, then show the assembled YAML.
    const [kcCluster, setKcCluster] = useState<Upstream | null>(null)
    const [kcAgentId, setKcAgentId] = useState('')
    const [kcYaml, setKcYaml] = useState('')
    const fileInputRef = useRef<HTMLInputElement>(null)
    const push = useToastStore((s) => s.push)

    const counter = useEventStore((s) => s.counters['upstream.created'])
    const delCounter = useEventStore((s) => s.counters['upstream.deleted'])

    const load = useCallback(() => {
      Promise.all([listUpstreams(), listAgents()])
        .then(([ups, ags]) => {
          setClusters((ups ?? []).filter((u) => u.kind === 'k8s'))
          setAgents(ags ?? [])
        })
        .catch((err) => {
          push('error', err instanceof ApiError ? err.message : 'Failed to load clusters')
        })
    }, [push])

    useEffect(load, [load, counter, delCounter])

    function openModal() {
      setDraft(emptyDraft)
      setOpen(true)
    }

    function openImportPicker() {
      fileInputRef.current?.click()
    }

    useImperativeHandle(ref, () => ({ openImport: openImportPicker, openAdd: openModal }), [])

    function buildAuth(d: ClusterDraft): ClusterAuthConfig {
      const auth: ClusterAuthConfig = { type: 'none', k8s_auth: d.authType }
      if (d.ca.trim()) auth.ca_bundle = d.ca
      if (d.authType === 'token') auth.token = d.token
      if (d.authType === 'client-cert') {
        auth.client_cert = d.clientCert
        auth.client_key = d.clientKey
      }
      if (d.authType === 'exec') {
        auth.exec_command = d.execCommand
        const args = d.execArgs
          .split('\n')
          .map((s) => s.trim())
          .filter(Boolean)
        if (args.length > 0) auth.exec_args = args
      }
      return auth
    }

    async function submit(e: React.FormEvent) {
      e.preventDefault()
      setBusy(true)
      try {
        await createCluster(draft.name, draft.baseURL, buildAuth(draft))
        push('success', 'Cluster created')
        setOpen(false)
        load()
      } catch (err) {
        push('error', err instanceof ApiError ? err.message : 'Failed to create cluster')
      } finally {
        setBusy(false)
      }
    }

    // "Import from kubeconfig" opens the OS file picker (the hidden input below); selecting a file
    // reads its text and uploads it. The operator picks the kubeconfig instead of us re-scanning a
    // fixed path.

    async function onImportFile(e: React.ChangeEvent<HTMLInputElement>) {
      const file = e.target.files?.[0]
      // Reset the input so selecting the same file again re-fires change.
      e.target.value = ''
      if (!file) return
      onImportingChange?.(true)
      try {
        const content = await file.text()
        const res = await importKubeconfigContent(content)
        push(
          'success',
          `Imported clusters — added ${(res.added ?? []).length}, updated ${(res.updated ?? []).length}, skipped ${(res.skipped ?? []).length}`,
        )
        load()
      } catch (err) {
        push('error', err instanceof ApiError ? err.message : 'Failed to import clusters')
      } finally {
        onImportingChange?.(false)
      }
    }

    async function remove(cluster: Upstream) {
      try {
        await deleteUpstream(cluster.name)
        push('success', 'Cluster deleted')
        setConfirmDelete(null)
        load()
      } catch (err) {
        push('error', err instanceof ApiError ? err.message : 'Failed to delete cluster')
      }
    }

    function openKubeconfig(cluster: Upstream) {
      setKcCluster(cluster)
      setKcAgentId(agents[0]?.id ?? '')
      setKcYaml('')
    }

    async function fetchKubeconfig() {
      if (!kcCluster) return
      const agent = agents.find((a) => a.id === kcAgentId)
      if (!agent) {
        push('error', 'Select an agent')
        return
      }
      // The daemon assembles the kubeconfig from an agent token; agents are write-once, so we do
      // not have the raw token here. The operator pastes the agent token they were given.
      const token = window.prompt(`Paste the bearer token for agent "${agent.name}"`)
      if (!token) return
      try {
        const res = await getKubeconfig(kcCluster.name, token.trim())
        setKcYaml(res.kubeconfig)
      } catch (err) {
        push('error', err instanceof ApiError ? err.message : 'Failed to build kubeconfig')
      }
    }

    function downloadKubeconfig() {
      if (!kcYaml || !kcCluster) return
      const blob = new Blob([kcYaml], { type: 'text/yaml' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${kcCluster.name}.kubeconfig.yaml`
      a.click()
      URL.revokeObjectURL(url)
    }

    return (
      <div className="space-y-6">
        <input
          ref={fileInputRef}
          type="file"
          accept=".yaml,.yml,.conf,.config,*"
          aria-label="Import kubeconfig file"
          className="hidden"
          onChange={onImportFile}
        />

        <section className="rounded-lg border border-border bg-card">
          <DataTable
            rows={clusters}
            rowKey={(c) => c.id}
            empty="No clusters yet — add one or import from kubeconfig"
            columns={[
              {
                header: 'Name',
                cell: (c) => (
                  <span className="inline-flex items-center gap-2">
                    {c.name}
                    {c.k8s_insecure && (
                      <span className="rounded bg-destructive/15 px-1.5 py-0 text-[11px] font-medium text-destructive">
                        insecure
                      </span>
                    )}
                  </span>
                ),
              },
              { header: 'API URL', cell: (c) => c.base_url, className: 'font-mono text-muted-foreground' },
              {
                header: 'Auth',
                cell: (c) =>
                  c.k8s_auth ? (
                    <StatusBadge status={c.k8s_auth} />
                  ) : (
                    <span
                      className="rounded bg-destructive/15 px-1.5 py-0 text-[11px] font-medium text-destructive"
                      title="No k8s credential — re-import this cluster's kubeconfig (Import from kubeconfig)"
                    >
                      ⚠ no auth
                    </span>
                  ),
              },
              {
                header: '',
                cell: (c) => (
                  <div className="flex justify-end gap-2">
                    <button
                      onClick={() => openKubeconfig(c)}
                      className="rounded bg-primary/15 px-2 py-0.5 text-[11px] font-medium text-primary hover:bg-primary/25"
                    >
                      Kubeconfig
                    </button>
                    <button
                      onClick={() => setConfirmDelete(c)}
                      className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                    >
                      Delete
                    </button>
                  </div>
                ),
              },
            ]}
          />
        </section>

        <Modal
          open={open}
          title="Add cluster"
          onClose={() => setOpen(false)}
          onSubmit={submit}
          footer={
            <>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={busy}
                className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
              >
                {busy ? '…' : 'Create'}
              </button>
            </>
          }
        >
          <FormField label="Name">
            <input
              className={fieldControlClass}
              value={draft.name}
              onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              placeholder="prod"
              aria-label="Name"
            />
          </FormField>
          <FormField label="API URL">
            <input
              className={fieldControlClass}
              value={draft.baseURL}
              onChange={(e) => setDraft({ ...draft, baseURL: e.target.value })}
              placeholder="https://api.k8s.example:6443"
              aria-label="API URL"
            />
          </FormField>
          <FormField label="CA bundle (PEM, optional)">
            <textarea
              className={`${fieldControlClass} h-20 font-mono`}
              value={draft.ca}
              onChange={(e) => setDraft({ ...draft, ca: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              aria-label="CA bundle"
            />
          </FormField>
          <FormField label="Auth type">
            <Select
              value={draft.authType}
              onChange={(t) => setDraft({ ...draft, authType: t as ClusterAuthType })}
              options={[
                { value: 'token', label: 'Token' },
                { value: 'client-cert', label: 'Client certificate' },
                { value: 'exec', label: 'Exec plugin' },
              ]}
            />
          </FormField>

          {draft.authType === 'token' && (
            <FormField label="Token">
              <input
                className={fieldControlClass}
                type="password"
                value={draft.token}
                onChange={(e) => setDraft({ ...draft, token: e.target.value })}
                aria-label="Token"
              />
            </FormField>
          )}
          {draft.authType === 'client-cert' && (
            <>
              <FormField label="Client certificate (PEM)">
                <textarea
                  className={`${fieldControlClass} h-20 font-mono`}
                  value={draft.clientCert}
                  onChange={(e) => setDraft({ ...draft, clientCert: e.target.value })}
                  aria-label="Client certificate"
                />
              </FormField>
              <FormField label="Client key (PEM)">
                <textarea
                  className={`${fieldControlClass} h-20 font-mono`}
                  value={draft.clientKey}
                  onChange={(e) => setDraft({ ...draft, clientKey: e.target.value })}
                  aria-label="Client key"
                />
              </FormField>
            </>
          )}
          {draft.authType === 'exec' && (
            <>
              <FormField label="Command">
                <input
                  className={fieldControlClass}
                  value={draft.execCommand}
                  onChange={(e) => setDraft({ ...draft, execCommand: e.target.value })}
                  placeholder="aws"
                  aria-label="Command"
                />
              </FormField>
              <FormField label="Args (one per line)">
                <textarea
                  className={`${fieldControlClass} h-20 font-mono`}
                  value={draft.execArgs}
                  onChange={(e) => setDraft({ ...draft, execArgs: e.target.value })}
                  placeholder={'eks\nget-token\n--cluster-name\nprod'}
                  aria-label="Args"
                />
              </FormField>
            </>
          )}
        </Modal>

        <Modal
          open={kcCluster !== null}
          title={kcCluster ? `Kubeconfig — ${kcCluster.name}` : 'Kubeconfig'}
          onClose={() => setKcCluster(null)}
          width="lg"
          footer={
            <>
              <button
                type="button"
                onClick={() => setKcCluster(null)}
                className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
              >
                Close
              </button>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={fetchKubeconfig}
                  className="rounded border border-border px-3 py-1.5 text-xs font-medium hover:bg-muted"
                >
                  Generate
                </button>
                <button
                  type="button"
                  onClick={downloadKubeconfig}
                  disabled={!kcYaml}
                  className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
                >
                  Download
                </button>
              </div>
            </>
          }
        >
          <FormField label="Agent">
            <Select
              value={kcAgentId}
              onChange={setKcAgentId}
              options={agents.map((a) => ({ value: a.id, label: a.name }))}
            />
          </FormField>
          {kcYaml ? (
            <pre className="max-h-72 overflow-auto rounded border border-border bg-muted p-2 font-mono text-[11px]">
              {kcYaml}
            </pre>
          ) : (
            <p className="text-xs text-muted-foreground">
              Pick the agent, click Generate, and paste that agent's bearer token when prompted. The
              kubeconfig carries only the agent's token — never the cluster's real credentials.
            </p>
          )}
        </Modal>

        <Modal
          open={confirmDelete !== null}
          title="Delete cluster"
          onClose={() => setConfirmDelete(null)}
          width="sm"
          footer={
            <>
              <button
                type="button"
                onClick={() => setConfirmDelete(null)}
                className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => confirmDelete && remove(confirmDelete)}
                className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90"
              >
                Delete
              </button>
            </>
          }
        >
          <p className="text-sm">
            Delete cluster <span className="font-mono">{confirmDelete?.name}</span>? Agents lose proxied
            access to it until it is re-added or re-imported.
          </p>
        </Modal>
      </div>
    )
  },
)
