package apns

import (
	"testing"

	"github.com/uniqush/goconf/conf"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// TestTheEndpointGateFailsClosedOnReconfiguration covers what happens the
// second time uniqush is handed a configuration.
//
// The setting is process-wide and SetPushServiceConfig is reachable more than
// once through the push service manager, so "read it if it parses" is not
// enough: a config that had enabled non-Apple endpoints, followed by one where
// the option was removed or corrupted, left the gate open because the second
// read wrote nothing at all. Deleting the line has to turn the capability off,
// which is the only reading of it an operator would expect.
func TestTheEndpointGateFailsClosedOnReconfiguration(t *testing.T) {
	previous := common.AllowsNonAppleEndpoints()
	t.Cleanup(func() { common.SetAllowNonAppleEndpoints(previous) })

	service := NewPushService()
	t.Cleanup(service.Finalize)

	apply := func(t *testing.T, value string, present bool) {
		t.Helper()
		file := conf.NewConfigFile()
		if present {
			file.AddOption("apns", "allow_non_apple_endpoints", value)
		}
		service.SetPushServiceConfig(push.NewPushServiceConfig(file, "apns"))
	}

	apply(t, "true", true)
	if !common.AllowsNonAppleEndpoints() {
		t.Fatal("Expected allow_non_apple_endpoints=true to enable non-Apple endpoints")
	}

	// The option removed entirely. This is the reconfiguration that used to
	// leave the gate open.
	apply(t, "", false)
	if common.AllowsNonAppleEndpoints() {
		t.Error("Removing allow_non_apple_endpoints left non-Apple endpoints enabled.\n" +
			"The setting is process-wide, so a config that no longer grants the capability " +
			"must take it away rather than silently preserve the previous answer.")
	}

	apply(t, "true", true)
	if !common.AllowsNonAppleEndpoints() {
		t.Fatal("Expected the option to enable non-Apple endpoints again")
	}

	// And a value that will not parse as a boolean. Unparseable is not a
	// licence to keep whatever was set before.
	apply(t, "yes-please", true)
	if common.AllowsNonAppleEndpoints() {
		t.Error("An unparseable allow_non_apple_endpoints left non-Apple endpoints enabled")
	}

	apply(t, "false", true)
	if common.AllowsNonAppleEndpoints() {
		t.Error("Expected allow_non_apple_endpoints=false to disable non-Apple endpoints")
	}
}
