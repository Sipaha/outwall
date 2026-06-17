# module: internal/client

A thin HTTP-over-unix-socket client for the daemon admin API. Dials the configured socket
path for every request; non-2xx responses are surfaced as the server's `{"error":...}`
message.

## Public API

- `New(socketPath string) *Client`
- `(*Client).Do(method, path string, body, out any) error` — JSON request/response; `body` and `out` may be nil.
