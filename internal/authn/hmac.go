package authn

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"net/http"
	"strconv"
	"time"

	"github.com/Sipaha/outwall/internal/upstream"
)

// hmacAuth signs outgoing requests with a keyed HMAC over a canonical string. This is outwall's own
// generic signing scheme (NOT a vendor standard); see ADR-0019. The canonical string is:
//
//	"{METHOD}\n{request-uri}\n{unix-timestamp}"
//
// where request-uri is r.URL.RequestURI() (path + "?" + raw query). The hex signature goes in the
// configured header (HMACHeader) and the timestamp in "X-Timestamp" so a verifier can reconstruct
// the canonical string. Algo is sha256 (default) or sha512.
type hmacAuth struct {
	secret []byte
	header string
	newH   func() hash.Hash
	now    func() time.Time // injectable for tests
}

func newHMACAuth(cfg upstream.AuthConfig) (*hmacAuth, error) {
	if cfg.HMACSecret == "" {
		return nil, fmt.Errorf("hmac: hmac_secret is required")
	}
	if cfg.HMACHeader == "" {
		return nil, fmt.Errorf("hmac: hmac_header is required")
	}
	var newH func() hash.Hash
	switch cfg.HMACAlgo {
	case "", "sha256":
		newH = sha256.New
	case "sha512":
		newH = sha512.New
	default:
		return nil, fmt.Errorf("hmac: unsupported algo %q (want sha256 or sha512)", cfg.HMACAlgo)
	}
	return &hmacAuth{secret: []byte(cfg.HMACSecret), header: cfg.HMACHeader, newH: newH, now: time.Now}, nil
}

// Apply computes the signature over the canonical string and sets the signature + timestamp headers.
func (h *hmacAuth) Apply(r *http.Request) error {
	ts := strconv.FormatInt(h.now().UTC().Unix(), 10)
	canonical := r.Method + "\n" + r.URL.RequestURI() + "\n" + ts
	mac := hmac.New(h.newH, h.secret)
	_, _ = mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))
	r.Header.Set(h.header, sig)
	r.Header.Set("X-Timestamp", ts)
	return nil
}
