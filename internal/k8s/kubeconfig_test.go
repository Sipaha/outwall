package k8s

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestKubeconfigRoundTrips(t *testing.T) {
	caPEM := "-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n"
	out, err := Kubeconfig("https://127.0.0.1:8080/prod-cluster", "prod-cluster", caPEM, "owa_agent_token")
	require.NoError(t, err)

	var kc struct {
		Clusters []struct {
			Cluster struct {
				Server                   string `yaml:"server"`
				CertificateAuthorityData string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			User struct {
				Token string `yaml:"token"`
			} `yaml:"user"`
		} `yaml:"users"`
		Contexts []struct {
			Name string `yaml:"name"`
		} `yaml:"contexts"`
		CurrentContext string `yaml:"current-context"`
	}
	require.NoError(t, yaml.Unmarshal(out, &kc))

	require.Len(t, kc.Clusters, 1)
	require.Equal(t, "https://127.0.0.1:8080/prod-cluster", kc.Clusters[0].Cluster.Server)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte(caPEM)), kc.Clusters[0].Cluster.CertificateAuthorityData)

	require.Len(t, kc.Users, 1)
	require.Equal(t, "owa_agent_token", kc.Users[0].User.Token)

	require.NotEmpty(t, kc.CurrentContext)
}
