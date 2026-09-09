// Package push_test holds the one test that has to live outside package push.
//
// destinationCarrier is sealed by having an unexported method, and the claim
// that seals it -- a lower-case method name is qualified by the package that
// declares it -- can only be demonstrated from a different package. This file
// is that different package.
package push_test

import (
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

// foreignError declares a method spelled exactly like the one
// push.destinationCarrier asks for, from outside package push.
type foreignError struct {
	dp *push.DeliveryPoint
}

func (e *foreignError) Error() string { return "an error from somewhere else" }

// pushDestination looks like the sealed method and is not it: this one is
// push_test.pushDestination, and the interface asks for push.pushDestination.
//
//nolint:unused // declared to prove it is not enough to satisfy the interface
func (e *foreignError) pushDestination() *push.DeliveryPoint { return e.dp }

// TestAForeignErrorCannotClaimADestination pins what the unexported method name
// buys.
//
// If it stopped holding -- if the method were exported, or the accessor took an
// exported interface -- any error from any package could name any device, and
// DestinationOf would hand that name to a log line an operator acts on. This
// test would still compile, and would start failing.
func TestAForeignErrorCannotClaimADestination(t *testing.T) {
	dp := push.NewEmptyDeliveryPoint()
	dp.FixedData["subscriber"] = "mallory"

	if got := push.DestinationOf(&foreignError{dp: dp}); got != nil {
		t.Errorf("An error declared outside package push named a device: %v.\n"+
			"destinationCarrier is sealed by its unexported method name; if that no longer holds, "+
			"any package can make uniqush attribute a failure to any subscriber.", got)
	}
}

// TestEmbeddingOneOfTheseErrorsKeepsItsDestination covers the one way a type
// outside this package does satisfy the interface, which is deliberate.
//
// Embedding promotes the method along with the field it reads, so the answer is
// the embedded error's own destination rather than a claim made about someone
// else's device.
func TestEmbeddingOneOfTheseErrorsKeepsItsDestination(t *testing.T) {
	dp := push.NewEmptyDeliveryPoint()
	dp.FixedData["subscriber"] = "alice"

	type wrapped struct{ *push.BadNotification }
	err := wrapped{push.NewBadNotificationForDeliveryPoint(dp, "BadPriority")}

	if got := push.DestinationOf(err); got != dp {
		t.Errorf("Expected an embedded error to keep naming its own device, got %v", got)
	}
}
