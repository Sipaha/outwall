package authn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/Sipaha/outwall/internal/upstream"
)

// emptyPayloadHash is SHA-256("") — the payload hash AWS expects for a body-less request.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sigv4Auth signs outgoing requests with AWS Signature Version 4 using static credentials. It is
// the authenticator for http upstreams with auth type "sigv4".
type sigv4Auth struct {
	signer  *v4.Signer
	creds   aws.Credentials
	region  string
	service string
	now     func() time.Time // injectable for tests
}

func newSigV4Auth(cfg upstream.AuthConfig) (*sigv4Auth, error) {
	if cfg.AWSAccessKeyID == "" || cfg.AWSSecretAccessKey == "" {
		return nil, fmt.Errorf("sigv4: aws_access_key_id and aws_secret_access_key are required")
	}
	if cfg.AWSRegion == "" || cfg.AWSService == "" {
		return nil, fmt.Errorf("sigv4: aws_region and aws_service are required")
	}
	return &sigv4Auth{
		signer:  v4.NewSigner(),
		creds:   aws.Credentials{AccessKeyID: cfg.AWSAccessKeyID, SecretAccessKey: cfg.AWSSecretAccessKey},
		region:  cfg.AWSRegion,
		service: cfg.AWSService,
		now:     time.Now,
	}, nil
}

// Apply computes the SHA-256 payload hash (restoring the request body so it can still be sent) and
// signs the request in place, setting Authorization + X-Amz-Date.
func (s *sigv4Auth) Apply(r *http.Request) error {
	payloadHash := emptyPayloadHash
	if r.Body != nil && r.Body != http.NoBody {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return fmt.Errorf("sigv4: read body: %w", err)
		}
		sum := sha256.Sum256(body)
		payloadHash = hex.EncodeToString(sum[:])
		// Restore the body for the actual round-trip.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	if err := s.signer.SignHTTP(r.Context(), s.creds, r, payloadHash, s.service, s.region, s.now().UTC()); err != nil {
		return fmt.Errorf("sigv4: sign: %w", err)
	}
	return nil
}
