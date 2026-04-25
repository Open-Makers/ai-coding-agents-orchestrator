package runner

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRateLimited is the sentinel returned (wrapped) by all runners when the
// underlying LLM provider rejects a request because of rate limiting, quota
// exhaustion, or session usage caps. Callers can detect any flavour with
// errors.Is(err, runner.ErrRateLimited) and stop the pipeline immediately —
// continuing would just burn more retries against the same exhausted limit.
var ErrRateLimited = errors.New("rate limited")

// RateLimitError carries provider-specific context about a rate-limit hit
// while still satisfying errors.Is(err, ErrRateLimited).
type RateLimitError struct {
	Provider string
	Detail   string
}

func (e *RateLimitError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: rate limit / quota exceeded", e.Provider)
	}
	return fmt.Sprintf("%s: rate limit / quota exceeded: %s", e.Provider, e.Detail)
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// rateLimitMarkers groups the case-insensitive substrings that indicate a
// provider response should be treated as a rate-limit / quota condition.
// The list is intentionally short and conservative — false positives would
// halt the pipeline on benign errors.
var rateLimitMarkers = []string{
	"rate limit",
	"rate-limit",
	"ratelimit",
	"rate_limit_error",
	"too many requests",
	"quota",
	"insufficient_quota",
	"usage limit",
	"usage_limit",
	"overloaded_error",
	"http 429",
	" 429 ",
	"status 429",
	"status: 429",
	"statuscode=429",
	"retry-after",
}

// ClassifyRateLimit returns a non-nil *RateLimitError when any of the given
// text fragments contains a known rate-limit marker. Returns nil otherwise.
func ClassifyRateLimit(provider string, fragments ...string) *RateLimitError {
	for _, frag := range fragments {
		if frag == "" {
			continue
		}
		lower := strings.ToLower(frag)
		for _, marker := range rateLimitMarkers {
			if strings.Contains(lower, marker) {
				return &RateLimitError{
					Provider: provider,
					Detail:   firstLine(frag),
				}
			}
		}
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
