# ADR-0026: Kubeconfig import — init-only auto-scan, update-on-reimport, k8s credential integrity

- **Status:** accepted
- **Date:** 2026-06-22

## Context

A registered k8s cluster (a Rancher endpoint) showed an empty **Auth** column and could not be
reached through the data plane. Root-causing it from the code exposed three linked problems:

1. **Its k8s credential had been overwritten.** The cluster was auto-imported correctly with
   `k8s_auth=token` (its kubeconfig user carries a `token`). Later an agent ran
   `request_host_access` on the k8s cluster; the pre-ADR-0025 code raised a host-access card with a
   *credential form*. The operator entered a token and approved → `applyApprovalSideEffects` called
   `upstream.SetAuth`, which replaces the **whole** auth config. The static `Authorization` header
   overwrote the cluster's k8s token: `auth_type=static`, `k8s_auth=""`. The k8s data plane builds
   its credential via `authn.buildK8sAuth` (which reads only `k8s_auth`), so it errored — the proxy
   returned an opaque `500 "auth config error"`. (ADR-0025 already stopped k8s host cards; this ADR
   adds the missing guard.)

2. **Re-importing couldn't repair it.** The importer is idempotent by **name** (ADR-0012): an
   existing cluster is *skipped, never mutated*. So an operator who notices a broken/rotated cluster
   has no way to refresh it — a re-import is a silent no-op. ADR-0012 itself flagged
   "update-on-reimport" as a future revisit.

3. **The auto-scan ran on every unlock.** It ran at `vault init` AND `vault unlock`. The
   skip-existing rule existed largely to make that repeated run safe. But re-scanning on every
   unlock is unnecessary and is the reason skip-existing had to be conservative.

## Decision

### Auto-scan runs ONLY at first start (vault init)

`autoImportClusters` is called from `hVaultInit` only; the call is removed from `hVaultUnlock`. First
run is exactly when the vault is empty, so the scan *seeds* clusters and nothing else. Unlock no
longer touches the kubeconfig. To pull in new or changed clusters later, the operator uses the
explicit **Import from kubeconfig** button.

### Explicit import updates an existing cluster in place

The importer gains an `update bool`. The init auto-scan passes `update=false` (skip existing — but
since it only runs on an empty vault, this is now moot). The explicit operator paths
(`POST /clusters/import`, body or rescan) pass `update=true`: an existing cluster is refreshed via
the new `upstream.Registry.UpdateTarget(id, baseURL, auth)` — server + auth on the **same upstream
ID**, so the cluster's policy rules survive (no delete+recreate that would orphan them). `Import` /
`ImportContent` now return `(added, updated, skipped)`; the UI toast reports all three.

### k8s credential integrity (defense-in-depth)

- The host-approve side effect refuses to `SetAuth` on a k8s upstream — attaching an HTTP credential
  to a cluster is rejected with a clear error (a card that, post-ADR-0025, should never exist).
- The proxy surfaces a k8s cluster with no usable credential as `502 "cluster credential not
  configured — operator must re-import its kubeconfig"` instead of an opaque 500.
- The Clusters UI flags a cluster with empty `k8s_auth` as **⚠ no auth** rather than a blank badge.

## Alternatives considered

- **Update existing on every import including the unlock auto-scan.** Rejected — re-running on every
  unlock and mutating in place would let a kubeconfig edit silently override operator changes on a
  routine unlock. The mutation is gated to an explicit operator action.
- **Delete + recreate the cluster on re-import.** Rejected — a new upstream ID orphans every policy
  rule that references the cluster. `UpdateTarget` keeps the ID.
- **Let `buildK8sAuth` fall back to the generic static-header credential for k8s.** Rejected — a k8s
  cluster's credential belongs in `k8s_auth` (a Rancher token IS a bearer token = k8s "token" auth);
  consuming a stray static header would paper over the mis-modeling instead of fixing it.
- **Keep auto-scan on unlock but make it cheap.** Rejected — there is no need to re-scan on unlock at
  all; init-only is simpler and removes the clobber surface entirely.

## Consequences

- The kubeconfig is read once, at init. Operators refresh/repair/rotate clusters via the explicit
  Import button, which now updates in place and preserves rules.
- A k8s cluster's credential can no longer be clobbered by a host-access approval, and a broken one
  is visible (UI badge) and explained (proxy error) rather than failing opaquely.
- `Importer.Import`/`ImportContent` signatures changed (`update` param, `updated` return) — all
  in-tree callers/tests updated; the `POST /clusters/import` response gained an `updated` array.
- the affected cluster is repaired by an explicit re-import (restoring `k8s_auth=token`).
