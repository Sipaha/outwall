// Package k8s decomposes raw Kubernetes API requests into the RBAC-relevant tuple and
// assembles agent kubeconfigs. It deliberately avoids k8s.io/client-go: the path parsing
// mirrors kube-apiserver's own RequestInfoResolver with pure string logic.
package k8s

import (
	"net/url"
	"strings"
)

// RequestInfo is the RBAC-relevant decomposition of a k8s API request.
type RequestInfo struct {
	IsResource  bool   // false for non-resource paths (/healthz, /version, /openapi/...)
	Namespace   string // "" = cluster-scoped or all-namespaces
	APIGroup    string // "" = core group
	Resource    string // e.g. "pods", "deployments"
	Subresource string // e.g. "log", "exec", "scale", "status" (else "")
	Name        string // resource name, "" for collections
	Verb        string // get|list|watch|create|update|patch|delete|deletecollection
}

// Parse decomposes a raw k8s API request (path already stripped of the /<cluster> prefix),
// mirroring kube-apiserver's RequestInfoResolver.
func Parse(method, path string, query url.Values) RequestInfo {
	segs := splitPath(path)
	if len(segs) == 0 {
		return RequestInfo{}
	}

	// Discovery / health / openapi paths are non-resource. The bare "/api" and "/apis"
	// roots are discovery too.
	switch segs[0] {
	case "api", "apis":
		// fallthrough to resource parsing below
	default:
		return RequestInfo{}
	}

	var rest []string
	var apiGroup string
	if segs[0] == "api" {
		// /api/<version>/...
		if len(segs) < 2 {
			return RequestInfo{} // bare /api
		}
		rest = segs[2:] // drop "api" and the version
	} else {
		// /apis/<group>/<version>/...
		if len(segs) < 3 {
			return RequestInfo{} // bare /apis or /apis/<group>
		}
		apiGroup = segs[1]
		rest = segs[3:] // drop "apis", group, version
	}

	ri := RequestInfo{IsResource: true, APIGroup: apiGroup}

	// Detect a namespaced request: rest = ["namespaces", <ns>, <resource>, ...].
	// The literal `namespaces` resource itself (/api/v1/namespaces/<name>) is cluster-scoped.
	if len(rest) >= 3 && rest[0] == "namespaces" {
		ri.Namespace = rest[1]
		rest = rest[2:]
	}

	// rest now = [<resource> [<name> [<subresource>]]].
	if len(rest) >= 1 {
		ri.Resource = rest[0]
	}
	if len(rest) >= 2 {
		ri.Name = rest[1]
	}
	if len(rest) >= 3 {
		ri.Subresource = rest[2]
	}

	ri.Verb = verbFor(method, ri.Name != "", ri.Subresource, query)
	return ri
}

// upgradeSubresources are the pod subresources that use an HTTP connection upgrade
// (WebSocket on modern clusters, SPDY on older ones) for a bidirectional stream:
// exec, attach, and port-forward. `kubectl cp` rides on exec (exec + tar), so it needs
// no separate entry.
var upgradeSubresources = map[string]bool{
	"exec":        true,
	"attach":      true,
	"portforward": true,
}

// IsUpgrade reports whether this request targets an interactive upgrade subresource
// (exec / attach / portforward). Such requests negotiate an HTTP connection upgrade and
// carry a duplex stream rather than a request/response body; the proxy streams them
// verbatim and the audit records metadata only.
func (ri RequestInfo) IsUpgrade() bool {
	return upgradeSubresources[ri.Subresource]
}

// verbFor derives the RBAC verb from method, whether a name is present, the subresource,
// and the query (watch/follow).
func verbFor(method string, named bool, subresource string, query url.Values) string {
	// The upgrade subresources (exec/attach/portforward) are negotiated as GET on modern
	// clusters (WebSocket) and POST on older ones (SPDY), but kube-apiserver authorizes them
	// uniformly as the `create` verb. Map them so a rule (ns, pods/exec, create) grants them
	// regardless of the wire method.
	if upgradeSubresources[subresource] {
		return "create"
	}
	switch strings.ToUpper(method) {
	case "POST":
		return "create"
	case "PUT":
		return "update"
	case "PATCH":
		return "patch"
	case "DELETE":
		if named {
			return "delete"
		}
		return "deletecollection"
	default: // GET, HEAD
		if isWatch(subresource, query) {
			return "watch"
		}
		if named {
			return "get"
		}
		return "list"
	}
}

// isWatch reports whether a GET is a watch/streaming read: ?watch=true on any collection,
// or ?follow=true on the pods/log subresource.
func isWatch(subresource string, query url.Values) bool {
	if truthy(query.Get("watch")) {
		return true
	}
	if subresource == "log" && truthy(query.Get("follow")) {
		return true
	}
	return false
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "true", "1":
		return true
	}
	return false
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}
