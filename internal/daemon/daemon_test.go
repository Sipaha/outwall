package daemon

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublishFansOut(t *testing.T) {
	d := newDaemon(t)
	ch, cancel := d.Subscribe()
	defer cancel()

	d.Publish("desktop.open-approvals", nil)

	select {
	case ev := <-ch:
		require.Equal(t, "desktop.open-approvals", ev.Type)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}
}

func TestBrowseDomainDefault(t *testing.T) {
	d := newDaemon(t)
	require.Equal(t, "outwall.localhost", d.cfg.BrowseDomain)
}

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
