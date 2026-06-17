package k8s

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		query  string
		want   RequestInfo
	}{
		{
			name:   "namespaced collection list",
			method: "GET",
			path:   "/api/v1/namespaces/prod/pods",
			want:   RequestInfo{IsResource: true, Namespace: "prod", Resource: "pods", Verb: "list"},
		},
		{
			name:   "namespaced named get",
			method: "GET",
			path:   "/api/v1/namespaces/prod/pods/web-1",
			want:   RequestInfo{IsResource: true, Namespace: "prod", Resource: "pods", Name: "web-1", Verb: "get"},
		},
		{
			name:   "log follow is watch",
			method: "GET",
			path:   "/api/v1/namespaces/prod/pods/web-1/log",
			query:  "follow=true",
			want:   RequestInfo{IsResource: true, Namespace: "prod", Resource: "pods", Name: "web-1", Subresource: "log", Verb: "watch"},
		},
		{
			name:   "all-namespaces watch",
			method: "GET",
			path:   "/api/v1/pods",
			query:  "watch=true",
			want:   RequestInfo{IsResource: true, Namespace: "", Resource: "pods", Verb: "watch"},
		},
		{
			name:   "patch grouped deployment",
			method: "PATCH",
			path:   "/apis/apps/v1/namespaces/prod/deployments/web",
			want:   RequestInfo{IsResource: true, Namespace: "prod", APIGroup: "apps", Resource: "deployments", Name: "web", Verb: "patch"},
		},
		{
			name:   "delete collection",
			method: "DELETE",
			path:   "/api/v1/namespaces/prod/pods",
			want:   RequestInfo{IsResource: true, Namespace: "prod", Resource: "pods", Verb: "deletecollection"},
		},
		{
			name:   "cluster-scoped nodes list",
			method: "GET",
			path:   "/api/v1/nodes",
			want:   RequestInfo{IsResource: true, Namespace: "", Resource: "nodes", Verb: "list"},
		},
		{
			name:   "namespace self get is cluster-scoped",
			method: "GET",
			path:   "/api/v1/namespaces/prod",
			want:   RequestInfo{IsResource: true, Namespace: "", Resource: "namespaces", Name: "prod", Verb: "get"},
		},
		{
			name:   "healthz is non-resource",
			method: "GET",
			path:   "/healthz",
			want:   RequestInfo{IsResource: false},
		},
		{
			name:   "version is non-resource",
			method: "GET",
			path:   "/version",
			want:   RequestInfo{IsResource: false},
		},
		{
			name:   "api root is non-resource",
			method: "GET",
			path:   "/api",
			want:   RequestInfo{IsResource: false},
		},
		{
			name:   "apis root is non-resource",
			method: "GET",
			path:   "/apis",
			want:   RequestInfo{IsResource: false},
		},
		{
			name:   "openapi is non-resource",
			method: "GET",
			path:   "/openapi/v2",
			want:   RequestInfo{IsResource: false},
		},
		{
			name:   "create pod is create",
			method: "POST",
			path:   "/api/v1/namespaces/prod/pods",
			want:   RequestInfo{IsResource: true, Namespace: "prod", Resource: "pods", Verb: "create"},
		},
		{
			name:   "put deployment is update",
			method: "PUT",
			path:   "/apis/apps/v1/namespaces/prod/deployments/web",
			want:   RequestInfo{IsResource: true, Namespace: "prod", APIGroup: "apps", Resource: "deployments", Name: "web", Verb: "update"},
		},
		{
			name:   "delete named pod is delete",
			method: "DELETE",
			path:   "/api/v1/namespaces/prod/pods/web-1",
			want:   RequestInfo{IsResource: true, Namespace: "prod", Resource: "pods", Name: "web-1", Verb: "delete"},
		},
		{
			name:   "named subresource scale get",
			method: "GET",
			path:   "/apis/apps/v1/namespaces/prod/deployments/web/scale",
			want:   RequestInfo{IsResource: true, Namespace: "prod", APIGroup: "apps", Resource: "deployments", Name: "web", Subresource: "scale", Verb: "get"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			require.NoError(t, err)
			got := Parse(tc.method, tc.path, q)
			require.Equal(t, tc.want, got)
		})
	}
}
