package daemon

import (
	"context"
	"crypto/subtle"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authenticate checks the caller's API key when keys are configured.
// With no configured keys every caller is allowed (auth disabled).
func (d *Daemon) authenticate(ctx context.Context) error {
	if len(d.keys) == 0 {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return errUnauthenticated()
	}
	presented := md.Get(MetadataHeader)
	for _, candidate := range presented {
		if d.keyAllowed(candidate) {
			return nil
		}
	}
	return errUnauthenticated()
}

// keyAllowed compares candidate against every configured key in constant
// time relative to the key bytes; the loop runs over all keys regardless
// of matches to avoid position-based timing leaks.
func (d *Daemon) keyAllowed(candidate string) bool {
	probe := []byte(candidate)
	allowed := 0
	for _, key := range d.keys {
		allowed |= subtle.ConstantTimeCompare(probe, key)
	}
	return allowed == 1
}

func errUnauthenticated() error {
	return status.Error(codes.Unauthenticated, "daemon: invalid or missing API key")
}
