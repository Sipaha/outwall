package access

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "acc.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestAccessRequestLifecycle(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create("a1", "u1", "triage issues")
	require.NoError(t, err)
	require.Equal(t, "pending", r.Status)

	p, err := reg.Pending()
	require.NoError(t, err)
	require.Len(t, p, 1)
	require.Equal(t, "triage issues", p[0].Purpose)

	require.NoError(t, reg.Resolve(r.ID, "granted"))
	require.ErrorIs(t, reg.Resolve("nope", "granted"), ErrNotFound)
	require.Error(t, reg.Resolve(r.ID, "bogus")) // invalid status

	p, _ = reg.Pending()
	require.Empty(t, p) // no longer pending
}

func TestGetByIDAndMarkRevoked(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create("a1", "u1", "triage issues")
	require.NoError(t, err)

	got, err := reg.GetByID(r.ID)
	require.NoError(t, err)
	require.Equal(t, r.ID, got.ID)
	require.Equal(t, "a1", got.AgentID)
	require.Equal(t, "u1", got.UpstreamID)

	_, err = reg.GetByID("nope")
	require.ErrorIs(t, err, ErrNotFound)

	require.Empty(t, got.ResolvedAt)
	require.NoError(t, reg.MarkRevoked(r.ID))
	got, err = reg.GetByID(r.ID)
	require.NoError(t, err)
	require.Equal(t, StatusRevoked, got.Status)
	require.NotEmpty(t, got.ResolvedAt)

	require.ErrorIs(t, reg.MarkRevoked("nope"), ErrNotFound)
}

func TestGrantLatest(t *testing.T) {
	reg := newReg(t)
	_, err := reg.Create("a1", "u1", "first")
	require.NoError(t, err)
	r2, err := reg.Create("a1", "u1", "second")
	require.NoError(t, err)

	ok, err := reg.GrantLatest("a1", "u1", nil)
	require.NoError(t, err)
	require.True(t, ok)

	// The latest pending row is now granted; the older one stays pending.
	latest, found, err := reg.Latest("a1", "u1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, r2.ID, latest.ID)
	require.Equal(t, StatusGranted, latest.Status)
	require.Empty(t, latest.Edits)

	// No pending row for a different (agent, upstream) → no-op.
	ok, err = reg.GrantLatest("a1", "other", nil)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestGrantLatestRecordsOperatorEdits(t *testing.T) {
	reg := newReg(t)
	_, err := reg.Create("a1", "u1", "read a record")
	require.NoError(t, err)

	edits := []BindingEdit{{Slot: "workspace", Requested: "*", Granted: "ECOSENT"}}
	ok, err := reg.GrantLatest("a1", "u1", edits)
	require.NoError(t, err)
	require.True(t, ok)

	latest, found, err := reg.Latest("a1", "u1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StatusGranted, latest.Status)
	require.Equal(t, edits, latest.Edits)
}

func TestDiffBindings(t *testing.T) {
	// No change → no edits.
	require.Empty(t, DiffBindings(
		map[string]string{"sourceId": "*", "workspace": "*"},
		map[string]string{"sourceId": "*", "workspace": "*"},
	))
	// One narrowed slot, reported; unchanged slot omitted.
	require.Equal(t,
		[]BindingEdit{{Slot: "workspace", Requested: "*", Granted: "ECOSENT"}},
		DiffBindings(
			map[string]string{"sourceId": "*", "workspace": "*"},
			map[string]string{"sourceId": "*", "workspace": "ECOSENT"},
		),
	)
	// Multiple edits are sorted by slot.
	require.Equal(t,
		[]BindingEdit{
			{Slot: "sourceId", Requested: "*", Granted: "emodel/ept-issue"},
			{Slot: "workspace", Requested: "*", Granted: "ECOSENT"},
		},
		DiffBindings(
			map[string]string{"sourceId": "*", "workspace": "*"},
			map[string]string{"sourceId": "emodel/ept-issue", "workspace": "ECOSENT"},
		),
	)
}

func TestMarkRevokedBySubjectUpstream(t *testing.T) {
	reg := newReg(t)
	_, err := reg.Create("ag1", "up1", "p1")
	require.NoError(t, err)
	_, err = reg.GrantLatest("ag1", "up1", nil)
	require.NoError(t, err)
	// A second, still-pending request for the same pair must NOT be revoked.
	_, err = reg.Create("ag1", "up1", "p2")
	require.NoError(t, err)

	n, err := reg.MarkRevokedBySubjectUpstream("ag1", "up1")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	all, err := reg.List()
	require.NoError(t, err)
	var revoked, pending int
	for _, req := range all {
		switch req.Status {
		case StatusRevoked:
			revoked++
		case StatusPending:
			pending++
		}
	}
	require.Equal(t, 1, revoked)
	require.Equal(t, 1, pending)
}
