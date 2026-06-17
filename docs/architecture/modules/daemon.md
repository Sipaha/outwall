# module: internal/daemon

Wires the store, vault, registries, and data-plane proxy together, and serves the data
plane (TCP localhost) plus a JSON admin API over a `0600` unix socket. The only package
that composes the others.

Admin endpoints: `POST /vault/init`, `POST /vault/unlock`, `GET /vault/status`,
`POST /upstreams`, `GET /upstreams` (secrets omitted), `POST /agents/register`,
`GET /agents`, `POST /grants`.

## Public API

- `Config struct { DBPath, SocketPath, Listen string }`
- `New(cfg Config) (*Daemon, error)` — opens the store, builds vault + registries + proxy (no listeners).
- `(*Daemon).AdminHandler() http.Handler` — the admin API mux.
- `(*Daemon).Serve(ctx context.Context) error` — runs both listeners until ctx is canceled.
- `(*Daemon).Close() error`.
