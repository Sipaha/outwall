# module: internal/tlsca

outwall's local certificate authority. On first run it generates a CA (ECDSA P-256) and
persists `ca.crt` / `ca.key` under the data dir at `0600`; on subsequent runs it loads the
same CA. It issues the data-plane server certificate, signed by the CA, so kubectl/client-go
validate the proxy honestly against the CA embedded in the agent kubeconfig — no
`--insecure-skip-tls-verify` (see ADR-0008 §7.1).

The daemon serves the data plane over TLS with `CA.ServerCert("127.0.0.1", "localhost", ...)`
and exposes `CA.CAPEM()` for the kubeconfig helper / MCP tool.

## Public API

- `LoadOrCreateCA(dir string) (*CA, error)` — idempotent; persists `ca.crt`/`ca.key` (0600).
- `(*CA).ServerCert(hosts ...string) (tls.Certificate, error)` — IP literals → IP SANs, names → DNS SANs.
- `(*CA).CAPEM() []byte` — the PEM-encoded CA certificate.
