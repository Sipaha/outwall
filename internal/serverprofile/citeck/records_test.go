package citeck

import "testing"

func TestRefSource(t *testing.T) {
	cases := []struct {
		ref, source, local string
	}{
		{"emodel/type@contract", "emodel/type", "contract"},
		{"type@contract", "type", "contract"},
		{"emodel/type@", "emodel/type", ""}, // create
		{"contract", "", "contract"},        // no '@' → whole thing is localId, empty source
		{"", "", ""},
	}
	for _, c := range cases {
		src, loc := refSource(c.ref)
		if src != c.source || loc != c.local {
			t.Fatalf("refSource(%q) = (%q,%q), want (%q,%q)", c.ref, src, loc, c.source, c.local)
		}
	}
}
