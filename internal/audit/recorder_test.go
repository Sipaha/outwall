package audit

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newRec(t *testing.T) *Recorder {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "au.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRecorder(s)
}

func TestRecordGetListPrune(t *testing.T) {
	rec := newRec(t)
	e := Entry{
		TS: time.Now().UTC(), AgentName: "claude", UpstreamName: "github", Method: "GET",
		Path: "/repos/x", StatusCode: 200, Decision: "allow",
		Headers: map[string]string{"Authorization": "***", "Accept": "application/json"},
	}
	require.NoError(t, rec.Record(e,
		Body{Kind: "request", ContentType: "application/json", Size: 3, Stored: []byte("{}!")},
		Body{Kind: "response", ContentType: "image/png", Size: 9000, Sha256: "abc", Stored: nil},
	))

	list, err := rec.List(50)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "github", list[0].UpstreamName)

	got, bodies, err := rec.Get(list[0].ID)
	require.NoError(t, err)
	require.Equal(t, "***", got.Headers["Authorization"]) // round-trips masked
	require.Len(t, bodies, 2)

	_, _, err = rec.Get("nope")
	require.ErrorIs(t, err, ErrNotFound)

	n, err := rec.Prune(time.Now().Add(time.Hour)) // everything older than now+1h
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	list, _ = rec.List(50)
	require.Empty(t, list)
}

func TestMaskHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("X-Api-Key", "k")
	h.Set("Accept", "application/json")
	m := MaskHeaders(h)
	require.Equal(t, "***", m["Authorization"])
	require.Equal(t, "***", m["X-Api-Key"])
	require.Equal(t, "application/json", m["Accept"])
}
