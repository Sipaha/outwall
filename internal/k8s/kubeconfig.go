package k8s

import (
	"encoding/base64"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Kubeconfig assembles an agent kubeconfig pointing at the outwall data plane. The agent's
// own outwall bearer token is the only credential; the cluster's real creds never appear here
// (see the design spec §7). serverURL is the data-plane URL including the /<cluster> prefix.
func Kubeconfig(serverURL, clusterName, caPEM, agentToken string) ([]byte, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("kubeconfig: cluster name required")
	}
	userName := clusterName + "-agent"
	contextName := clusterName

	doc := map[string]any{
		"apiVersion":      "v1",
		"kind":            "Config",
		"current-context": contextName,
		"clusters": []map[string]any{{
			"name": clusterName,
			"cluster": map[string]any{
				"server":                     serverURL,
				"certificate-authority-data": base64.StdEncoding.EncodeToString([]byte(caPEM)),
			},
		}},
		"users": []map[string]any{{
			"name": userName,
			"user": map[string]any{
				"token": agentToken,
			},
		}},
		"contexts": []map[string]any{{
			"name": contextName,
			"context": map[string]any{
				"cluster": clusterName,
				"user":    userName,
			},
		}},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal kubeconfig: %w", err)
	}
	return out, nil
}
