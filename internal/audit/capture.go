package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// BodyCap is the maximum number of body bytes retained per captured body.
const BodyCap = 256 * 1024

// cappedCapture is a streaming tee over a ReadCloser: reads pass through unchanged,
// up to cap bytes are retained, total counts every byte read, and onClose fires once
// (on the first Close) with the retained bytes, total, and truncated=(total>cap).
type cappedCapture struct {
	src       io.ReadCloser
	cap       int
	buf       []byte
	total     int64
	onClose   func([]byte, int64, bool)
	closeOnce sync.Once
}

// NewCapture wraps src in a capped streaming tee. onClose may be nil.
func NewCapture(src io.ReadCloser, capBytes int, onClose func([]byte, int64, bool)) io.ReadCloser {
	return &cappedCapture{src: src, cap: capBytes, onClose: onClose}
}

// Capture is a capped streaming tee that also exposes its captured state, for callers
// (like the proxy) that must read the retained bytes at a different point than Close.
type Capture struct{ c *cappedCapture }

// NewCaptureRef wraps src and returns the ReadCloser plus a handle to read the
// captured bytes/total/truncated at any time (e.g. after the body has been fully read).
func NewCaptureRef(src io.ReadCloser, capBytes int) (io.ReadCloser, *Capture) {
	cc := &cappedCapture{src: src, cap: capBytes}
	return cc, &Capture{c: cc}
}

// Captured returns the retained bytes, total observed size, and whether truncation occurred.
func (c *Capture) Captured() (stored []byte, total int64, truncated bool) {
	return c.c.buf, c.c.total, c.c.total > int64(c.c.cap)
}

func (c *cappedCapture) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	if n > 0 {
		c.total += int64(n)
		if room := c.cap - len(c.buf); room > 0 {
			take := n
			if take > room {
				take = room
			}
			c.buf = append(c.buf, p[:take]...)
		}
	}
	return n, err
}

func (c *cappedCapture) Close() error {
	c.closeOnce.Do(func() {
		if c.onClose != nil {
			c.onClose(c.buf, c.total, c.total > int64(c.cap))
		}
	})
	return c.src.Close()
}

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
