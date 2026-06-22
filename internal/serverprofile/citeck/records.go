// Package citeck is the server-profile plugin for Citeck ECOS upstreams. It classifies and gates
// Records API requests (POST /api/records/{query,mutate,delete}). It is the ONLY package in outwall
// permitted to name "citeck" (see ADR-0034); the core stays platform-agnostic.
package citeck

import "strings"

// refSource splits an EntityRef "appName/sourceId@localId" into its source part (keeping the
// optional "appName/" prefix, which operator globs match against) and its localId. With no '@' the
// whole string is the localId and the source is empty (mirrors EntityRef.valueOf).
func refSource(ref string) (source, localID string) {
	at := strings.IndexByte(ref, '@')
	if at < 0 {
		return "", ref
	}
	return ref[:at], ref[at+1:]
}
