// Package email abstracts OTP delivery behind a small interface, mirroring
// the internal/llm provider pattern — swapping delivery mechanisms (SMTP
// relay, a transactional API) is an env var change, not a code change.
package email

import "context"

type Provider interface {
	SendOTP(ctx context.Context, to, code string) error
}
