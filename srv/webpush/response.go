package webpush

import (
	"net/http"
	"strconv"
	"time"
)

// outcome classifies what uniqush should do about a push server's response.
type outcome int

const (
	// outcomeSuccess: the push server accepted the message.
	outcomeSuccess outcome = iota
	// outcomeUnsubscribe: this endpoint is permanently dead. Remove it.
	outcomeUnsubscribe
	// outcomeBadNotification: our request was wrong. Retrying the same bytes
	// will fail identically, so surface it to the caller instead.
	outcomeBadNotification
	// outcomeRetry: transient. Worth trying again later.
	outcomeRetry
)

// classifyStatus maps an RFC 8030 push response onto an outcome.
//
// Status semantics, from RFC 8030 and the UnifiedPush server spec
// (https://unifiedpush.org/developers/spec/server/):
//
//	2xx  accepted. The spec mandates 201 but tells application servers to
//	     "accept status code from 200-299 as a 201", so the whole range counts.
//	404  RFC 8030 §7.3: the push subscription has expired. Permanently dead.
//	410  Gone. Not literally specified for this path by RFC 8030, which reserves
//	     410 for delivery receipts, but it is what every real push server
//	     (Mozilla autopush, FCM, ntfy) returns for a revoked subscription, so
//	     treating it as dead is both conventional and correct in practice.
//	400  malformed request. Our bug, not a dead endpoint: do not unsubscribe.
//	413  payload too large. Also our problem; retrying unchanged cannot help.
//	429  rate limited. The spec asks that limits be honoured per endpoint rather
//	     than per host.
//	3xx  the spec says redirects MUST NOT be followed, so a redirect reaching
//	     here means the push server did something unexpected.
//	else unknown; may be retried at the application server's discretion.
func classifyStatus(statusCode int) outcome {
	switch {
	case statusCode >= 200 && statusCode <= 299:
		return outcomeSuccess
	case statusCode == http.StatusNotFound, statusCode == http.StatusGone:
		return outcomeUnsubscribe
	case statusCode == http.StatusBadRequest, statusCode == http.StatusRequestEntityTooLarge:
		return outcomeBadNotification
	case statusCode == http.StatusTooManyRequests:
		return outcomeRetry
	case statusCode >= 500:
		return outcomeRetry
	default:
		return outcomeRetry
	}
}

// retryAfter reads the Retry-After header, which RFC 8030 says a push server
// SHOULD send with a 429. Both the delay-seconds and HTTP-date forms are legal.
// Returns 0 when absent or unparseable, leaving the caller to pick a default.
func retryAfter(header http.Header, now time.Time) time.Duration {
	value := header.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := when.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
