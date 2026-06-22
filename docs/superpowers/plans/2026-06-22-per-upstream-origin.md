# Per-upstream origin (subdomain data plane) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a browser (Playwright) load a full web app through outwall by giving each http upstream its own **origin** — `https://<name>.outwall.localhost:<port>/…` — instead of a path prefix on one shared origin, while the existing path-prefix model keeps working unchanged for API clients and kubectl.

**Architecture:** The data plane keeps one loopback listener. The proxy routes by the `Host` header: a `<name>.<browse-domain>` host selects the upstream and forwards the **full** path (so root-relative/relative URLs resolve to the same upstream); a `127.0.0.1`/`localhost` host keeps the path-prefix model. TLS serves a per-SNI leaf cert issued on demand from the local CA (covers dotted upstream names; Chromium resolves `*.localhost` to loopback natively). In host (browse) mode only, the proxy rewrites the `Location` header and strips `Set-Cookie; Domain=` so login redirects and cookies stay on the per-upstream origin. The agent discovers the browse URL via `get_access`/`list_upstreams`.

**Tech Stack:** Go (CGO-free server), `crypto/tls` `GetCertificate`, `net/http/httputil.ReverseProxy`; React UI is touched only to surface the browse URL (optional, last task).

**Companion ADR to write during this plan:** ADR-0035 (per-upstream origin / subdomain data plane). Spec: `docs/superpowers/specs/2026-06-22-server-profiles-and-per-upstream-origin-design.md` (Part D). This plan is the sequel to `2026-06-22-server-profiles-citeck.md` (Parts A/B/C, already shipped). Builds on ADR-0033 (cookie token + Sec-Fetch-Site CSRF), which explicitly deferred "a distinct origin per upstream" to here.

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` in every import.
- **No `citeck`** in the core — the only sanctioned exception remains `internal/serverprofile/citeck` + the `"citeck"` profile-name value (ADR-0034). This plan touches the core data plane; keep it citeck-free.
- No CGO in the server binary (`CGO_ENABLED=0`). No panics / `log.Fatal` in library code — return wrapped `error` (`%w`). `log/slog` for logging.
- Add new deps at `@latest`; do not bump existing deps. (This plan needs no new deps.)
- Alpha: no storage-format back-compat to preserve. (This plan adds no schema.)
- Commit author MUST be `Sipaha <sipahabk@gmail.com>`: `git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit`. **No `Co-Authored-By`. No `git commit --amend`.** New commits only. Remote `origin` (ssh alias `github.com-sipaha`), branch `main`.
- Gate before each commit: `gofmt -w .`, `go vet ./...`, `go test ./... -race`, `CGO_ENABLED=0 go build ./...`, and the desktop build `go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop`. For any web task also `cd web && pnpm test && pnpm lint && pnpm build`, then `git checkout -- internal/daemon/webdist/index.html`.

## Key facts the implementation depends on

- The data-plane TLS today uses a single static cert with SANs `127.0.0.1`, `localhost`, `::1`
  (`daemon.go:207-214`); `tlsca.CA.ServerCert(hosts ...string)` issues a CA-signed leaf
  (`tlsca/ca.go:127`). A `*.outwall.localhost` wildcard would NOT cover a dotted upstream name like
  `enterprise.ecos24.ru.outwall.localhost` (wildcard matches one label) — so we issue a **per-SNI**
  leaf instead.
- Chromium resolves any `*.localhost` host to loopback natively (no `/etc/hosts` needed); it only
  needs to trust the outwall CA (or run with `ignoreHTTPSErrors`). Non-Chromium clients/curl would
  need `--resolve`, but they use the path-prefix model anyway.
- The proxy selects the upstream from the path today (`proxy.go:131-142`) and the reverse proxy
  forwards `singleJoin(base.Path, rest)` (`proxy.go:343`). `ModifyResponse` is audit-only
  (`proxy.go:375`).
- Discovery returns `BasePath: "/"+name` in `mcpsvc.AccessResult` (`service.go:90,162,…`); the
  daemon wires the data-plane base URL via `SetKubeconfigParams("https://"+cfg.Listen, …)`
  (`service.go:60-63`).

## File structure (created / modified)

- **Modify** `internal/daemon/daemon.go` — `Config.BrowseDomain` + default; data-plane TLS uses a `GetCertificate` callback; pass the browse domain to the proxy and to mcpsvc.
- **Modify** `internal/cli/root.go` — `--browse-domain` flag.
- **Modify** `internal/tlsca/ca.go` — `ServerCertFor(sni)` with an issued-cert cache (+ keep `ServerCert`).
- **Create** `internal/tlsca/sni.go` (+ test) OR add to `ca.go` — the per-SNI cache + selection.
- **Modify** `internal/proxy/proxy.go` — `Deps.BrowseDomain`; Host-based routing; browse-mode `Location`/`Set-Cookie` rewriting.
- **Modify** `internal/mcpsvc/service.go` — `browseDomain`/origin params + `AccessResult.BrowseURL`.
- **Modify** `internal/daemon/admin.go` — `hUpstreamList` returns `browse_url` for http upstreams.
- **Modify** `web/src/lib/types.ts` + `web/src/pages/Upstreams.tsx` (+ tests) — show the browse URL (optional, last task).
- **Create** `docs/architecture/decisions/0035-per-upstream-origin.md`; **modify** `docs/INDEX.md`.

---

### Task 1: Config — browse domain

**Files:**
- Modify: `internal/daemon/daemon.go` (`Config` struct ~52-79; defaults in `New` ~110-125; `const DefaultBrowseDomain`)
- Modify: `internal/cli/root.go` (flag block ~38-44; wire into the `daemon.Config`)
- Test: `internal/daemon/daemon_test.go` (or wherever Config defaulting is tested) — add a default test.

**Interfaces:**
- Produces: `Config.BrowseDomain string`; `const DefaultBrowseDomain = "outwall.localhost"`; CLI flag `--browse-domain`.

- [ ] **Step 1: Write the failing test**

```go
func TestBrowseDomainDefault(t *testing.T) {
	d := newDaemon(t) // existing helper that builds a Daemon with a temp DB
	require.Equal(t, "outwall.localhost", d.cfg.BrowseDomain)
}
```

> If `newDaemon` lives in `admin_test.go` and `d.cfg` is unexported but same-package, this works (`package daemon`). If no Config-defaulting test exists yet, this is the first.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestBrowseDomainDefault -v`
Expected: FAIL — `BrowseDomain` undefined.

- [ ] **Step 3: Write minimal implementation**

In `daemon.go`, add the const near the other defaults and the field to `Config`:

```go
const DefaultBrowseDomain = "outwall.localhost"
```
```go
	// BrowseDomain is the base domain for per-upstream browser origins: an http upstream <name> is
	// reachable at https://<name>.<BrowseDomain>:<port>/ (Host-routed). Default "outwall.localhost"
	// (Chromium resolves *.localhost to loopback). The path-prefix model on 127.0.0.1 is unchanged.
	BrowseDomain string
```

In `New`, default it (next to the other `if cfg.X == "" { cfg.X = DefaultX }` guards):

```go
	if cfg.BrowseDomain == "" {
		cfg.BrowseDomain = DefaultBrowseDomain
	}
```

In `cli/root.go`, add the flag and pass it through to the `daemon.Config` the CLI builds:

```go
	root.PersistentFlags().StringVar(&gf.browseDomain, "browse-domain", daemon.DefaultBrowseDomain, "base domain for per-upstream browser origins")
```
(add `browseDomain string` to the `gf` struct, and set `BrowseDomain: gf.browseDomain` where the Config is constructed.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -run TestBrowseDomainDefault -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/ internal/cli/
git add internal/daemon/daemon.go internal/cli/root.go internal/daemon/daemon_test.go
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(daemon): browse-domain config for per-upstream origins"
```

---

### Task 2: tlsca — per-SNI server cert with cache

**Files:**
- Modify: `internal/tlsca/ca.go` (add the cache + selector)
- Test: `internal/tlsca/ca_test.go` (or new `sni_test.go`)

**Interfaces:**
- Consumes: existing `CA.ServerCert(hosts ...string)`.
- Produces: `CA.ServerCertFor(sni string) (*tls.Certificate, error)` — returns a cached leaf whose SAN is `sni` (plus loopback IPs), issuing+caching on first use; concurrency-safe.

- [ ] **Step 1: Write the failing test**

```go
func TestServerCertForCachesPerSNI(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	require.NoError(t, err)

	c1, err := ca.ServerCertFor("be.outwall.localhost")
	require.NoError(t, err)
	require.Contains(t, c1.Leaf.DNSNames, "be.outwall.localhost")

	// A dotted name (an upstream host) is covered exactly (a one-label wildcard could not).
	c2, err := ca.ServerCertFor("enterprise.ecos24.ru.outwall.localhost")
	require.NoError(t, err)
	require.Contains(t, c2.Leaf.DNSNames, "enterprise.ecos24.ru.outwall.localhost")

	// Same SNI returns the cached identical cert (same leaf pointer / serial).
	c1b, err := ca.ServerCertFor("be.outwall.localhost")
	require.NoError(t, err)
	require.Equal(t, c1.Leaf.SerialNumber, c1b.Leaf.SerialNumber)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tlsca/ -run TestServerCertForCachesPerSNI -v`
Expected: FAIL — `ServerCertFor` undefined.

- [ ] **Step 3: Write minimal implementation**

Add a cache to the `CA` struct and the method. Add `sync` to the imports.

```go
// CA is a loaded local certificate authority.
type CA struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	caPEM []byte

	sniMu    sync.Mutex
	sniCache map[string]*tls.Certificate
}
```

> Initialize `sniCache` lazily in `ServerCertFor` (don't change `loadCA`/`createCA` constructors).

```go
// ServerCertFor returns a CA-signed leaf whose SAN is the given SNI host (plus the loopback IPs so
// the same endpoint still answers IP/localhost handshakes). Certs are issued once per SNI and
// cached for the CA's lifetime. The SNI is a hostname (never an IP) — callers pass the browse host.
func (c *CA) ServerCertFor(sni string) (*tls.Certificate, error) {
	c.sniMu.Lock()
	defer c.sniMu.Unlock()
	if c.sniCache == nil {
		c.sniCache = map[string]*tls.Certificate{}
	}
	if cert, ok := c.sniCache[sni]; ok {
		return cert, nil
	}
	leaf, err := c.ServerCert(sni, "127.0.0.1", "::1")
	if err != nil {
		return nil, err
	}
	c.sniCache[sni] = &leaf
	return &leaf, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tlsca/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/tlsca/
git add internal/tlsca/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(tlsca): per-SNI server cert issuance with cache"
```

---

### Task 3: daemon — data-plane TLS via GetCertificate (static IP cert + per-SNI browse certs)

**Files:**
- Modify: `internal/daemon/daemon.go` (`Serve`, the `dataTLS` block ~204-214)
- Test: `internal/daemon/daemon_test.go` — test the cert-selection callback directly.

**Interfaces:**
- Consumes: `CA.ServerCert` (static), `CA.ServerCertFor` (Task 2), `Config.BrowseDomain` (Task 1).
- Produces: a `tls.Config{GetCertificate}` that returns the static loopback cert for an IP/`localhost`/empty SNI and a per-SNI browse cert for a `*.<BrowseDomain>` SNI. Factor the selection into a testable method `func (d *Daemon) dataPlaneCert(hello *tls.ClientHelloInfo) (*tls.Certificate, error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestDataPlaneCertSelection(t *testing.T) {
	d := newDaemon(t) // BrowseDomain defaults to outwall.localhost

	// Browse SNI → a cert carrying that SAN.
	c, err := d.dataPlaneCert(&tls.ClientHelloInfo{ServerName: "be.outwall.localhost"})
	require.NoError(t, err)
	require.Contains(t, c.Leaf.DNSNames, "be.outwall.localhost")

	// localhost / empty SNI → the static loopback cert (has 127.0.0.1).
	c2, err := d.dataPlaneCert(&tls.ClientHelloInfo{ServerName: "localhost"})
	require.NoError(t, err)
	require.NotEmpty(t, c2.Leaf.IPAddresses)

	c3, err := d.dataPlaneCert(&tls.ClientHelloInfo{ServerName: ""})
	require.NoError(t, err)
	require.NotEmpty(t, c3.Leaf.IPAddresses)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestDataPlaneCertSelection -v`
Expected: FAIL — `dataPlaneCert` undefined.

- [ ] **Step 3: Write minimal implementation**

Issue the static loopback cert once when the daemon is constructed (store it on the `Daemon`), or lazily in the callback. Add a field `staticCert *tls.Certificate` to `Daemon` and populate it in `New` (after the CA is loaded):

```go
	sc, err := ca.ServerCert("127.0.0.1", "localhost", "::1")
	if err != nil {
		return nil, fmt.Errorf("issue data-plane server cert: %w", err)
	}
	d.staticCert = &sc
```

Add the selection method:

```go
// dataPlaneCert picks the TLS leaf for a handshake: a per-SNI browse cert when the SNI is a
// <name>.<BrowseDomain> host, else the static loopback cert (IP/localhost/empty SNI, used by API
// clients and kubectl).
func (d *Daemon) dataPlaneCert(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	sni := hello.ServerName
	if sni != "" && strings.HasSuffix(sni, "."+d.cfg.BrowseDomain) {
		return d.ca.ServerCertFor(sni)
	}
	return d.staticCert, nil
}
```
(add `strings` to daemon.go imports if needed.)

In `Serve`, replace the static `dataTLS` with the callback (remove the now-duplicate `ServerCert` call there since the static cert is issued in `New`):

```go
	dataTLS := &tls.Config{
		GetCertificate: d.dataPlaneCert,
		MinVersion:     tls.VersionTLS12,
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/
git add internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(daemon): data-plane TLS picks per-SNI browse cert vs static loopback cert"
```

---

### Task 4: proxy — Host-based routing for browse origins

**Files:**
- Modify: `internal/proxy/proxy.go` (`Deps` struct ~27-37; `New`/`handler` ~39-50; `ServeHTTP` upstream-selection ~131-154)
- Modify: `internal/daemon/daemon.go` — pass `BrowseDomain` into `proxy.New(Deps{...})`.
- Test: `internal/proxy/proxy_test.go`.

**Interfaces:**
- Consumes: `Config.BrowseDomain`.
- Produces: `Deps.BrowseDomain string` (stored on `handler`); a request to `Host: <name>.<browse-domain>` selects upstream `<name>` and forwards the FULL path; `127.0.0.1`/`localhost` Host keeps the path-prefix model. A `browse bool` local is computed for Task 5.

- [ ] **Step 1: Write the failing test**

```go
func TestProxyHostRoutesToUpstream(t *testing.T) {
	h, _, up, pol, _, _ := build(t) // existing harness; build sets BrowseDomain (see Step 3 note)
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer backend.Close()
	u, err := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	allowOp(t, pol, u.ID, "GET", "/dashboard") // existing helper: allow GET /dashboard
	tok := newAgentToken(t) // mirror however proxy_test mints an agent token

	// Host-routed (browse) request: no /<name> prefix in the path; full path forwarded.
	r := httptest.NewRequest("GET", "https://be.outwall.localhost/dashboard", nil)
	r.Host = "be.outwall.localhost"
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "/dashboard", gotPath) // full path forwarded, NOT stripped
}
```

> Adapt `newAgentToken`/`allowOp` to the real helpers in `proxy_test.go`. If `build(t)` does not set `BrowseDomain`, update the harness to construct `proxy.New(Deps{..., BrowseDomain: "outwall.localhost"})` (see Step 3).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestProxyHostRoutesToUpstream -v`
Expected: FAIL — without Host routing the path-splitter treats `dashboard` as the upstream name → 404 (unknown upstream).

- [ ] **Step 3: Write minimal implementation**

Add the field to `Deps` and `handler`, and thread it through `New`:

```go
type Deps struct {
	// …existing fields…
	BrowseDomain string // base domain for per-upstream browser origins (Host-routed); "" disables it
}
```
(store `BrowseDomain` on the `handler` struct and set it in `New`.)

In `daemon.go`, where `proxy.New(Deps{...})` is built, add `BrowseDomain: cfg.BrowseDomain`.

In `ServeHTTP`, before the path split, try Host routing:

```go
	// Browse (Host-routed) origin: a request to <name>.<BrowseDomain> selects the upstream by Host
	// and forwards the FULL path, so a browser's root-relative/absolute URLs resolve to the same
	// upstream. The 127.0.0.1/localhost path-prefix model (API clients, kubectl) is unchanged.
	var name, rest string
	browse := false
	host := r.Host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		host = hh
	}
	if suffix := "." + h.BrowseDomain; h.BrowseDomain != "" && strings.HasSuffix(host, suffix) {
		name = strings.TrimSuffix(host, suffix)
		rest = strings.TrimPrefix(r.URL.Path, "/")
		browse = true
	} else {
		trimmed := strings.TrimPrefix(r.URL.Path, "/")
		name, rest, _ = strings.Cut(trimmed, "/")
	}
	if name == "" {
		writeErr(w, http.StatusNotFound, "no upstream in path")
		return
	}
	up, err := h.Upstreams.GetByName(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown upstream")
		return
	}
	relPath := "/" + rest
	// escRelPath: in browse mode the full escaped path is the relative path; otherwise strip the
	// "/<name>" prefix as before.
	escRelPath := relPath
	if browse {
		escRelPath = r.URL.EscapedPath()
		if escRelPath == "" {
			escRelPath = "/"
		}
	} else if esc := r.URL.EscapedPath(); strings.HasPrefix(esc, "/"+name) {
		escRelPath = strings.TrimPrefix(esc, "/"+name)
		if escRelPath == "" {
			escRelPath = "/"
		}
	}
```

(Replace the existing `name, rest, _ := strings.Cut(...)` + `up := GetByName` + `relPath`/`escRelPath` block with the above. Add `net` to imports. The downstream code using `up`, `rest`, `relPath`, `escRelPath`, and a new `browse` is otherwise unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/ -race -v`
Expected: PASS (existing path-prefix tests still green — they use `127.0.0.1`/default Host, so `browse=false`).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/proxy/ internal/daemon/
git add internal/proxy/proxy.go internal/proxy/proxy_test.go internal/daemon/daemon.go
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(proxy): Host-based routing for per-upstream browse origins"
```

---

### Task 5: proxy — browse-mode Location + Set-Cookie rewriting

**Files:**
- Modify: `internal/proxy/proxy.go` (the reverse-proxy build ~286-380, `ModifyResponse`)
- Test: `internal/proxy/proxy_test.go`

**Interfaces:**
- Consumes: the `browse bool` and `up`/`base` from Task 4.
- Produces: in browse mode only, the response `Location` header (if it points at the upstream's real origin) is rewritten to the browse origin `https://<name>.<BrowseDomain>:<port>`, and every `Set-Cookie` has its `Domain=` attribute stripped (so the cookie binds to the browse origin). Audit capture is unchanged. The path-prefix mode is byte-for-byte unchanged.

- [ ] **Step 1: Write the failing test**

```go
func TestProxyBrowseRewritesLocationAndCookieDomain(t *testing.T) {
	h, _, up, pol, _, _ := build(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect to the backend's own absolute origin + set a domain-scoped cookie.
		w.Header().Set("Location", "http://"+r.Host+"/after-login")
		w.Header().Add("Set-Cookie", "sid=abc; Path=/; Domain="+r.Host+"; HttpOnly")
		w.WriteHeader(302)
	}))
	defer backend.Close()
	u, err := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	allowOp(t, pol, u.ID, "GET", "/login")
	tok := newAgentToken(t)

	r := httptest.NewRequest("GET", "https://be.outwall.localhost/login", nil)
	r.Host = "be.outwall.localhost"
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, 302, w.Code)
	// Location rewritten to the browse origin (host, not the backend's real host).
	require.Contains(t, w.Header().Get("Location"), "be.outwall.localhost")
	require.NotContains(t, w.Header().Get("Location"), "127.0.0.1:") // not the backend host:port
	require.Equal(t, "/after-login", mustURLPath(t, w.Header().Get("Location")))
	// Set-Cookie Domain stripped (so the browser keeps it on the browse origin).
	require.NotContains(t, w.Header().Get("Set-Cookie"), "Domain=")
	require.Contains(t, w.Header().Get("Set-Cookie"), "sid=abc")
}
```

> Add a tiny `mustURLPath` helper (parse the URL, return `.Path`) in the test file if one doesn't exist.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestProxyBrowseRewrites -v`
Expected: FAIL — Location still points at the backend host; `Domain=` still present.

- [ ] **Step 3: Write minimal implementation**

The reverse proxy is built per request around `base` (the upstream URL) and `browse`. Set a `ModifyResponse` that, in browse mode, rewrites the two headers, then runs the existing audit capture. Compute the browse origin once:

```go
	browseOrigin := "" // e.g. https://be.outwall.localhost (port added from the request below)
	if browse {
		browseOrigin = "https://" + r.Host // r.Host includes the :port the browser used
	}
```

Add a helper (package-level) and wire it into `ModifyResponse`. Structure `ModifyResponse` so it ALWAYS runs in browse mode (even when audit capture is off), doing the header rewrite first, then the existing capture if enabled:

```go
	needModify := browse || captureBodies // captureBodies is the existing audit condition
	if needModify {
		rp.ModifyResponse = func(resp *http.Response) error {
			if browse {
				rewriteBrowseHeaders(resp, base, browseOrigin)
			}
			if captureBodies {
				// …the existing audit body-capture block, unchanged…
			}
			return nil
		}
	}
```
```go
// rewriteBrowseHeaders rewrites response headers so a browser stays on the per-upstream browse
// origin: a Location pointing at the upstream's real origin becomes the browse origin (relative
// Locations are left untouched, they resolve correctly), and every Set-Cookie loses its Domain
// attribute so the cookie binds to the browse origin instead of the upstream's real host.
func rewriteBrowseHeaders(resp *http.Response, base *url.URL, browseOrigin string) {
	if loc := resp.Header.Get("Location"); loc != "" {
		if u, err := url.Parse(loc); err == nil && u.IsAbs() && strings.EqualFold(u.Host, base.Host) {
			u.Scheme, u.Host = "", "" // make it relative to the browse origin
			resp.Header.Set("Location", strings.TrimPrefix(browseOrigin+u.String(), ""))
		}
	}
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
		resp.Header.Del("Set-Cookie")
		for _, c := range cookies {
			resp.Header.Add("Set-Cookie", stripCookieDomain(c))
		}
	}
}

// stripCookieDomain removes a Domain=… attribute from a Set-Cookie header value, leaving the rest
// (Path, HttpOnly, Secure, SameSite, …) intact, so the cookie becomes host-only on the browse origin.
func stripCookieDomain(setCookie string) string {
	parts := strings.Split(setCookie, ";")
	out := parts[:0]
	for _, p := range parts {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), "domain=") {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ";")
}
```

> Note on the `Location` rewrite: setting `u.Scheme/u.Host = ""` and prefixing `browseOrigin` yields `https://<host>/after-login`. Verify with the test that the result path is `/after-login` and the host is the browse host.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/ -race -v`
Expected: PASS (path-prefix tests unaffected — `browse=false`, so `rewriteBrowseHeaders` never runs and `ModifyResponse` keeps its original audit-only behavior).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/proxy/
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(proxy): browse-mode Location + Set-Cookie domain rewriting"
```

---

### Task 6: discovery — BrowseURL in get_access + list_upstreams

**Files:**
- Modify: `internal/mcpsvc/service.go` (`AccessResult` ~88-95; a `browseURL` helper; set it in `GetAccess`/`RequestHostAccess`/`RequestAccess` results; a `SetBrowseDomain`/param setter ~60-63)
- Modify: `internal/daemon/daemon.go` — call the new setter with `cfg.BrowseDomain` (next to `SetKubeconfigParams`).
- Modify: `internal/daemon/admin.go` (`hUpstreamList` ~336-364) — add `browse_url` for http upstreams.
- Test: `internal/mcpsvc/service_test.go` and `internal/daemon/admin_test.go`.

**Interfaces:**
- Consumes: `Config.BrowseDomain`, the data-plane port (derivable from the existing `dataPlaneURL`).
- Produces: `AccessResult.BrowseURL string` (`json:"browse_url,omitempty"`) = `https://<name>.<BrowseDomain>:<port>` for http upstreams (empty for k8s or when the name isn't a valid DNS host); `GET /upstreams` returns `browse_url` for http upstreams.

- [ ] **Step 1: Write the failing test**

```go
// in mcpsvc service_test.go — mirror the existing test that builds a Service + SetKubeconfigParams.
func TestGetAccessIncludesBrowseURL(t *testing.T) {
	s := newService(t) // existing helper
	s.SetKubeconfigParams("https://127.0.0.1:8099", "ca")
	s.SetBrowseDomain("outwall.localhost")
	// register an http upstream "be" + an allow rule for the agent so status is granted/open…
	// (mirror the existing get_access test setup)
	res, err := s.GetAccess(agentID, "be")
	require.NoError(t, err)
	require.Equal(t, "https://be.outwall.localhost:8099", res.BrowseURL)
}
```

> Adapt to the real `newService` helper + how the existing `GetAccess` test arranges a granted upstream. If the helper names differ, follow `service_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpsvc/ -run TestGetAccessIncludesBrowseURL -v`
Expected: FAIL — `SetBrowseDomain`/`BrowseURL` undefined.

- [ ] **Step 3: Write minimal implementation**

Add the field + setter + helper to the Service:

```go
// in AccessResult:
	BrowseURL string `json:"browse_url,omitempty"` // https://<name>.<browse-domain>:<port> for http upstreams
```
```go
	browseDomain string // base domain for per-upstream browse origins
```
```go
// SetBrowseDomain provides the base domain for per-upstream browse URLs surfaced to agents.
func (s *Service) SetBrowseDomain(domain string) { s.browseDomain = domain }

// browseURL returns the per-upstream browser origin for an http upstream, or "" when browsing does
// not apply (no browse domain configured, a k8s cluster, or a name that is not a valid DNS host).
func (s *Service) browseURL(up *upstream.Upstream) string {
	if s.browseDomain == "" || up.Kind == upstream.KindK8s || !isDNSHost(up.Name) {
		return ""
	}
	port := portOf(s.dataPlaneURL) // dataPlaneURL is e.g. https://127.0.0.1:8099
	host := up.Name + "." + s.browseDomain
	if port == "" {
		return "https://" + host
	}
	return "https://" + host + ":" + port
}
```

Add small helpers `portOf(rawURL string) string` (parse, return `u.Port()`) and `isDNSHost(name string) bool` (true when `name` is non-empty and every char is `[A-Za-z0-9.-]` and it has no `@`/`/` — k8s/exec names with `@` are excluded). Set `res.BrowseURL = s.browseURL(up)` in the agent-facing result builders (`GetAccess`, `RequestHostAccess`, `RequestAccess` — wherever the final `AccessResult` is returned to the agent; you do NOT need it on every internal `stPending` literal, just the returned result).

In `daemon.go`, next to `svc.SetKubeconfigParams(...)`, add `svc.SetBrowseDomain(cfg.BrowseDomain)`.

In `admin.go` `hUpstreamList`, in the non-k8s branch add the browse URL (compute the same way; the daemon has `cfg.BrowseDomain` and the data-plane port from `cfg.Listen`):

```go
		} else {
			m["auth"] = u.Auth.Public()
			if bu := d.browseURLFor(u); bu != "" {
				m["browse_url"] = bu
			}
			// …existing logged_in…
		}
```
add a small `func (d *Daemon) browseURLFor(u *upstream.Upstream) string` mirroring `browseURL` (reuse the same `isDNSHost`/port logic; you may put the shared helpers in a small file to avoid duplication, or accept a 5-line duplicate — prefer a shared helper).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpsvc/ ./internal/daemon/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/mcpsvc/ internal/daemon/
git add internal/mcpsvc/ internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(discovery): surface per-upstream browse_url in get_access + list_upstreams"
```

---

### Task 7: web — show the browse URL on the Hosts screen (optional surface)

**Files:**
- Modify: `web/src/lib/types.ts` (`Upstream` interface — add `browse_url?: string`)
- Modify: `web/src/pages/Upstreams.tsx` — render the browse URL (a copyable link/text) for http upstreams that have one.
- Test: `web/src/pages/Upstreams.test.tsx`.

**Interfaces:**
- Consumes: `Upstream.browse_url` from `GET /upstreams` (Task 6).

- [ ] **Step 1: Write the failing test**

```ts
it('shows the browse URL for an http upstream that has one', async () => {
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([
    { id: 'u1', name: 'be', base_url: 'https://be.test', auth_type: 'none', browse_url: 'https://be.outwall.localhost:8099' },
  ])
  vi.spyOn(api, 'getProfiles').mockResolvedValue([])
  render(<Upstreams />)
  expect(await screen.findByText('https://be.outwall.localhost:8099')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Upstreams`
Expected: FAIL — browse URL not rendered.

- [ ] **Step 3: Write minimal implementation**

Add `browse_url?: string` to the `Upstream` interface in `types.ts`. In `Upstreams.tsx`, in the host row (where `base_url`/badges render), when `u.browse_url` is set render it as a small monospace/copyable line labelled e.g. "Browse:". Match the existing row styling; do not add a copy library — a plain `<a>`/`<span>` is fine.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test -- Upstreams && pnpm lint && pnpm build && cd ..` then `git checkout -- internal/daemon/webdist/index.html`
Expected: PASS, lint+build clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/pages/Upstreams.tsx web/src/pages/Upstreams.test.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): show per-upstream browse URL on the Hosts screen"
```

---

### Task 8: full gate + ADR-0035 + verification note

**Files:**
- Create: `docs/architecture/decisions/0035-per-upstream-origin.md`
- Modify: `docs/INDEX.md` (ADR list)

- [ ] **Step 1: Run the full gate**

```bash
gofmt -w . && go vet ./... && go test ./... -race && CGO_ENABLED=0 go build ./...
go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop
cd web && pnpm test && pnpm lint && pnpm build && cd ..
git checkout -- internal/daemon/webdist/index.html
```
Expected: all green.

- [ ] **Step 2: Write ADR-0035**

Create `docs/architecture/decisions/0035-per-upstream-origin.md` (status accepted, date 2026-06-22). Cover: the Host-routed per-upstream origin (`<name>.<browse-domain>`) alongside the unchanged path-prefix model; per-SNI cert issuance (why not a one-label wildcard — dotted upstream names; Chromium resolves `*.localhost` to loopback); the bounded browse-mode response rewriting (Location + Set-Cookie Domain) and the deliberate decision NOT to rewrite HTML/CSS/JS content (root-relative URLs resolve under the per-upstream origin; JS-built absolute URLs to the real host remain the known residual); how this **closes the ADR-0033 single-origin confused-deputy** (each upstream is now a distinct origin, so cross-upstream `fetch` is cross-site and the `Sec-Fetch-Site` guard rejects the cookie); discovery via `browse_url`. Note residual limits and that non-Chromium clients need `--resolve` (they use path-prefix anyway).

- [ ] **Step 3: Link ADR in INDEX**

Add the ADR-0035 line to the `decisions/` list in `docs/INDEX.md`.

- [ ] **Step 4: Commit + push**

```bash
git add docs/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "docs(adr-0035): per-upstream origin (subdomain data plane)"
git push origin main
```

- [ ] **Step 5: Manual verification (record the result, don't fake it)**

With a rebuilt app (`make run`) and `enterprise.ecos24.ru` registered + OIDC-logged-in + a citeck/raw-http browse rule approved, drive Playwright with a context that trusts the outwall CA (or `ignoreHTTPSErrors: true`), set the `outwall_token` cookie for `https://enterprise.ecos24.ru.outwall.localhost:<port>`, navigate there, and confirm the app renders (a real authenticated page loads through outwall). If a blocker remains, record the exact symptom. (This step is operator-driven — the daemon can't drive the operator's browser/login; surface the result to the user.)

---

## Self-review notes

- **Spec coverage (Part D):** same-listener Host routing → Task 4; per-SNI TLS → Tasks 2-3; browse-mode Location/Set-Cookie rewrite → Task 5; discovery `browse_url` → Tasks 6-7; config → Task 1; ADR + confused-deputy closure note → Task 8.
- **Non-breaking:** every task keeps the path-prefix model (`127.0.0.1`/`localhost` Host, `browse=false`) byte-for-byte — existing proxy/kubectl/API tests must stay green. Each task asserts that.
- **Naming consistency:** `Config.BrowseDomain`/`DefaultBrowseDomain` (`outwall.localhost`), `CA.ServerCertFor`, `Daemon.dataPlaneCert`/`staticCert`, `Deps.BrowseDomain`, `handler.browse`, `rewriteBrowseHeaders`/`stripCookieDomain`, `AccessResult.BrowseURL`/`browse_url`, `Service.SetBrowseDomain`/`browseURL`, `isDNSHost`/`portOf` — used identically across tasks.
- **Security:** per-upstream origin strengthens isolation (closes the ADR-0033 residual). Cookie auth + `Sec-Fetch-Site` guard are unchanged; the `outwall_token` cookie is now per-origin. No new ambient authority.
- **Out of scope:** HTML/CSS/JS content rewriting (documented residual); any change to the policy model (browse traffic is gated by the same rules — raw-http or a profile — as before).
</content>
