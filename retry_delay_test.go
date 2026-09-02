package main

import (
	"testing"
	"time"
)

// TestMaxRequestedRetryDelayCoversApplesFloor ties the cap to the one delay a
// backend in this tree legitimately asks for.
//
// APNs answers TooManyProviderTokenUpdates with a 20-minute floor and the push
// cannot succeed before it clears, so a cap at or below that would silently
// convert the retry into a dropped notification -- the exact failure seeding
// from RetryError.After was introduced to fix.
func TestMaxRequestedRetryDelayCoversApplesFloor(t *testing.T) {
	const applesMintFloor = 20 * time.Minute

	if maxRequestedRetryDelay <= applesMintFloor {
		t.Errorf("maxRequestedRetryDelay is %v, which does not leave room for Apple's %v "+
			"provider-token floor; a push refused with TooManyProviderTokenUpdates would be "+
			"dropped instead of retried.", maxRequestedRetryDelay, applesMintFloor)
	}

	// And bounded: the point of the cap is that a Retry-After header cannot
	// pin a goroutine and a notification for an unreasonable time. fcm and
	// unifiedpush both parse that header without an upper bound of their own.
	if maxRequestedRetryDelay > time.Hour {
		t.Errorf("maxRequestedRetryDelay is %v; a retry holds a live goroutine, a timer and the "+
			"notification, so a remote server should not be able to reserve them for that long.",
			maxRequestedRetryDelay)
	}
}
