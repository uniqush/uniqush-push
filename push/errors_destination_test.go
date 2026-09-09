package push

import (
	"errors"
	"testing"
	"time"
)

// Tests for DestinationOf, the accessor #265 is really about.
//
// The complaint in that issue is a log line reading "Subscriber=Unknown
// DeliveryPoint=Unknown Failed: Bad Notification: DeviceTokenNotForTopic": an
// error about one device, reported without naming it. The answer has to travel
// with the error, because the paths that report one are not always the paths
// that know which device a push was for.

func destinationTestDeliveryPoint() *DeliveryPoint {
	dp := NewEmptyDeliveryPoint()
	dp.FixedData["subscriber"] = "alice"
	dp.FixedData["devtoken"] = "0123456789abcdef"
	return dp
}

// TestDestinationOfFindsTheDevice covers every error that is about one device.
func TestDestinationOfFindsTheDevice(t *testing.T) {
	dp := destinationTestDeliveryPoint()
	psp := NewEmptyPushServiceProvider()

	errs := map[string]error{
		"ErrorReport":               NewErrorForDeliveryPoint(dp, "boom"),
		"ErrorReport (formatted)":   NewErrorfForDeliveryPoint(dp, "boom %d", 1),
		"BadNotification":           NewBadNotificationForDeliveryPoint(dp, "DeviceTokenNotForTopic"),
		"ConnectionError":           NewConnectionErrorForDeliveryPoint(dp, errors.New("no route to host")),
		"BadDeliveryPoint":          NewBadDeliveryPointWithDetails(dp, "NoDevtoken"),
		"UnsubscribeUpdate":         NewUnsubscribeUpdate(psp, dp),
		"InvalidRegistrationUpdate": NewInvalidRegistrationUpdate(psp, dp),
		"DeliveryPointUpdate":       NewDeliveryPointUpdate(dp),
		"RetryError":                NewRetryError(psp, dp, NewEmptyNotification(), time.Second),
	}

	for name, err := range errs {
		if got := DestinationOf(err); got != dp {
			t.Errorf("%s: expected the delivery point it was built with, got %v", name, got)
		}
	}
}

// TestDestinationOfIsNilWhenThereIsNoDevice is the other half, and the reason
// the caller cannot simply print whatever it gets back.
//
// Some errors are legitimately about no device: rejected credentials are about
// every device in the service at once, and an InfoReport is a message. Claiming
// a device for those would put a name in a log line that has nothing to do with
// what went wrong.
func TestDestinationOfIsNilWhenThereIsNoDevice(t *testing.T) {
	psp := NewEmptyPushServiceProvider()

	errs := map[string]error{
		"BadPushServiceProvider": NewBadPushServiceProviderWithDetails(psp, "expired certificate"),
		"InfoReport":             NewInfo("something worth saying"),
		"ErrorReport":            NewError("boom"),
		"BadNotification":        NewBadNotificationWithDetails("payload too large"),
		"ConnectionError":        NewConnectionError(errors.New("no route to host")),
		"a plain error":          errors.New("not a push error at all"),
		"nil":                    nil,
	}

	for name, err := range errs {
		if got := DestinationOf(err); got != nil {
			t.Errorf("%s: expected no delivery point, got %v", name, got)
		}
	}
}
