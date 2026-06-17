package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"strings"
)

// BodyCap is the maximum number of body bytes retained per captured body.
const BodyCap = 256 * 1024

// maskedHeaderNames are header names whose values are always masked.
var maskedHeaderNames = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
}

// maskedSubstrings: any header whose (lowercased) name contains one of these is masked.
var maskedSubstrings = []string{"api-key", "apikey", "token", "secret"}

func shouldMask(name string) bool {
	lower := strings.ToLower(name)
	if maskedHeaderNames[lower] {
		return true
	}
	for _, s := range maskedSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// MaskHeaders flattens an http.Header to a single-value map with sensitive values
// replaced by "***" (see Global Constraints / ADR-0004).
func MaskHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for name, vals := range h {
		if shouldMask(name) {
			out[name] = "***"
			continue
		}
		out[name] = strings.Join(vals, ", ")
	}
	return out
}

// isTextContentType reports whether a body of this Content-Type should be stored as bytes.
// An empty Content-Type is treated as text (the caller only stores when the body is non-empty).
func isTextContentType(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	mediaType = strings.ToLower(mediaType)
	switch {
	case strings.HasPrefix(mediaType, "text/"):
		return true
	case mediaType == "application/json",
		mediaType == "application/xml",
		mediaType == "application/x-www-form-urlencoded":
		return true
	case strings.HasPrefix(mediaType, "application/") &&
		(strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")):
		return true
	}
	return false
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ClassifyBody builds a Body from captured bytes. Text bodies keep their stored bytes;
// non-text bodies keep metadata only (Stored=nil). Sha256 is over the stored bytes only,
// computed when there are any stored bytes.
func ClassifyBody(kind, contentType string, stored []byte, total int64, truncated bool) Body {
	b := Body{
		Kind:        kind,
		ContentType: contentType,
		Size:        total,
		Truncated:   truncated,
	}
	if len(stored) > 0 {
		b.Sha256 = sha256Hex(stored)
	}
	if isTextContentType(contentType) {
		b.Stored = stored
	} else {
		b.Stored = nil
		b.Truncated = false // metadata-only: nothing to truncate
	}
	return b
}
