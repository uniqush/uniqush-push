package fcm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uniqush/uniqush-push/push"
)

// FCM HTTP v1 error codes, from
// https://firebase.google.com/docs/reference/fcm/rest/v1/ErrorCode
const (
	// errUnregistered: the app instance was uninstalled or the token expired.
	// The only unambiguous "this device is gone" signal.
	errUnregistered = "UNREGISTERED"
	// errSenderIDMismatch: the token belongs to a different Firebase project.
	// It will never work for us, so it is equally permanent.
	errSenderIDMismatch = "SENDER_ID_MISMATCH"
	// errInvalidArgument: something in the request was wrong. Usually ours.
	errInvalidArgument = "INVALID_ARGUMENT"
	// errQuotaExceeded: rate limited.
	errQuotaExceeded = "QUOTA_EXCEEDED"
	// errUnavailable: the server is overloaded.
	errUnavailable = "UNAVAILABLE"
	// errInternal: an unknown server-side error.
	errInternal = "INTERNAL"
	// errThirdPartyAuth: the APNs certificate or web push key uploaded to
	// Firebase is bad. An operator problem, not a per-token one.
	errThirdPartyAuth = "THIRD_PARTY_AUTH_ERROR"
)

const (
	// defaultQuotaRetry is the minimum backoff Google's scaling guide asks for
	// after a 429 with no Retry-After.
	defaultQuotaRetry = 60 * time.Second
	// defaultServerRetry is the floor for retrying a 5xx. The same guide asks
	// for at least 10 seconds before any retry.
	defaultServerRetry = 10 * time.Second
)

// errorResponse is the google.rpc.Status envelope v1 returns on failure.
type errorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// fcmErrorCode digs the FCM-specific code out of the details array.
//
// The top-level "status" is a generic gRPC code; the actionable one lives in
// the detail whose @type is google.firebase.fcm.v1.FcmError. Falling back to
// the generic status matters because UNREGISTERED has no generic equivalent,
// but INVALID_ARGUMENT and the retryable codes do.
func (r *errorResponse) fcmErrorCode() string {
	for _, detail := range r.Error.Details {
		if strings.HasSuffix(detail.Type, "FcmError") && detail.ErrorCode != "" {
			return detail.ErrorCode
		}
	}
	return r.Error.Status
}

// successResponse is what a 200 returns: the assigned message name.
type successResponse struct {
	Name string `json:"name"`
}

// interpretResponse maps one v1 response onto a uniqush error.
//
// The mapping is deliberately conservative about unsubscribing. The legacy API
// had NotRegistered and InvalidRegistration as separate signals; v1 collapses
// most bad input into INVALID_ARGUMENT, which covers an oversized payload and a
// non-string data value as well as a malformed token. Treating that as "the
// device is gone" would delete working subscriptions because of a bug in the
// caller's payload, so only UNREGISTERED and SENDER_ID_MISMATCH unsubscribe.
func (ps *pushService) interpretResponse(response *http.Response, psp *push.PushServiceProvider, dp *push.DeliveryPoint, notif *push.Notification, msgID *string) push.Error {
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return push.NewErrorf("Could not read the FCM response: %v", err)
	}

	if response.StatusCode == http.StatusOK {
		success := new(successResponse)
		if err := json.Unmarshal(body, success); err == nil && success.Name != "" {
			*msgID = fmt.Sprintf("%v:%v", psp.Name(), success.Name)
		}
		return nil
	}

	failure := new(errorResponse)
	if err := json.Unmarshal(body, failure); err != nil {
		// A non-JSON body means something between us and FCM answered, which is
		// exactly what the decommissioned legacy endpoint does: it returns an
		// HTML 404 from the Google frontend. Say so, because "invalid character
		// '<'" is a baffling thing to find in a log.
		return push.NewErrorf(
			"FCM returned HTTP %d with a non-JSON body (%.120q). "+
				"If this is a 404, the push service provider may still be configured for the "+
				"legacy API, which was decommissioned on 2024-06-20",
			response.StatusCode, strings.TrimSpace(string(body)))
	}

	code := failure.fcmErrorCode()
	message := failure.Error.Message
	if message == "" {
		message = code
	}

	switch code {
	case errUnregistered:
		// The device is gone. This is the replacement for NotRegistered.
		return push.NewUnsubscribeUpdate(psp, dp)

	case errSenderIDMismatch:
		// Valid token, wrong project. It can never work here, so drop it.
		return push.NewUnsubscribeUpdate(psp, dp)

	case errInvalidArgument:
		// Deliberately not an unsubscribe. See the doc comment above.
		return push.NewBadNotificationWithDetails(fmt.Sprintf("FCM rejected the request: %s", message))

	case errQuotaExceeded:
		return push.NewRetryErrorWithReason(psp, dp, notif,
			retryAfter(response.Header, time.Now(), defaultQuotaRetry),
			fmt.Errorf("FCM quota exceeded: %s", message))

	case errUnavailable, errInternal:
		return push.NewRetryErrorWithReason(psp, dp, notif,
			retryAfter(response.Header, time.Now(), defaultServerRetry),
			fmt.Errorf("FCM is unavailable: %s", message))

	case errThirdPartyAuth:
		// Not the token's fault and not retryable: the credentials uploaded to
		// the Firebase project are wrong.
		return push.NewBadPushServiceProviderWithDetails(psp,
			fmt.Sprintf("FCM rejected the credentials configured in the Firebase project: %s", message))
	}

	// Unrecognised code. Retry on 5xx, since that is the safer reading of an
	// unknown server-side failure; surface anything else.
	if response.StatusCode >= 500 {
		return push.NewRetryErrorWithReason(psp, dp, notif,
			retryAfter(response.Header, time.Now(), defaultServerRetry),
			fmt.Errorf("FCM returned HTTP %d: %s", response.StatusCode, message))
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return push.NewBadPushServiceProviderWithDetails(psp,
			fmt.Sprintf("FCM rejected our credentials (HTTP %d): %s", response.StatusCode, message))
	}
	return push.NewErrorf("FCMError: %s (HTTP %d)", message, response.StatusCode)
}

// retryAfter reads the Retry-After header, falling back to a default.
func retryAfter(header http.Header, now time.Time, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := when.Sub(now); delay > 0 {
			return delay
		}
	}
	return fallback
}
