package apns

import (
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests for the bundleid a /subscribe may set on one device.

// subscribeDeviceWithBundleID builds a delivery point the way /subscribe does.
func subscribeDeviceWithBundleID(t *testing.T, kv map[string]string) *push.DeliveryPoint {
	t.Helper()
	ensureAPNSRegistered()

	data := map[string]string{
		"pushservicetype": "apns",
		"service":         "environments",
		"subscriber":      "alice",
		"devtoken":        "0123456789abcdef",
	}
	for key, value := range kv {
		data[key] = value
	}
	dp, err := push.GetPushServiceManager().BuildDeliveryPointFromMap(data)
	if err != nil {
		t.Fatalf("Could not build a delivery point: %v", err)
	}
	return dp
}

// TestSubscribeRecordsADeviceBundleID covers the parameter itself, and where it
// is kept.
//
// VolatileData, because a delivery point's name hashes its fixed data: a device
// that corrected its bundle id would otherwise become a second subscription
// beside the one it already had, and the first would go on being pushed to.
func TestSubscribeRecordsADeviceBundleID(t *testing.T) {
	withBundle := subscribeDeviceWithBundleID(t, map[string]string{"bundleid": "com.example.app.enterprise"})
	if got := withBundle.VolatileData["bundleid"]; got != "com.example.app.enterprise" {
		t.Errorf("Expected the device's bundle id to be recorded, got %q", got)
	}
	if _, fixed := withBundle.FixedData["bundleid"]; fixed {
		t.Error("The bundle id is in FixedData, so changing it would create a second subscription " +
			"rather than update this one")
	}

	// And the name does not depend on it, which is the same statement from the
	// other side: this is what lets a device be re-subscribed with a corrected
	// bundle id.
	withoutBundle := subscribeDeviceWithBundleID(t, nil)
	if withBundle.Name() != withoutBundle.Name() {
		t.Errorf("The bundle id changed the delivery point's name (%q vs %q), so re-subscribing "+
			"with one would leave the old subscription in place",
			withBundle.Name(), withoutBundle.Name())
	}
}

// TestSubscribeClearsADeviceBundleID checks removing it puts the device back on
// the provider's, the way an omitted bundleid does at /addpsp.
func TestSubscribeClearsADeviceBundleID(t *testing.T) {
	dp := subscribeDeviceWithBundleID(t, map[string]string{"bundleid": "com.example.app.enterprise"})

	// A later /subscribe for the same device, sending an empty value.
	if err := NewPushService().BuildDeliveryPointFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         "environments",
		"subscriber":      "alice",
		"devtoken":        "0123456789abcdef",
		"bundleid":        "  ",
	}, dp); err != nil {
		t.Fatalf("Could not rebuild the delivery point: %v", err)
	}
	if got, ok := dp.VolatileData["bundleid"]; ok {
		t.Errorf("Expected an empty bundleid to clear it, got %q", got)
	}
}

// TestBundleIDForDeliveryPointPrefersTheDevice states the precedence on its
// own, since it is the rule the push path is built on.
func TestBundleIDForDeliveryPointPrefersTheDevice(t *testing.T) {
	psp := buildProvider(t, nil) // bundleid com.example.environments
	device := subscribeDeviceWithBundleID(t, map[string]string{"bundleid": "com.example.app.enterprise"})
	plain := subscribeDeviceWithBundleID(t, nil)

	if got := common.BundleIDForDeliveryPoint(psp, device); got != "com.example.app.enterprise" {
		t.Errorf("Expected the device's bundle id to win, got %q", got)
	}
	if got := common.BundleIDForDeliveryPoint(psp, plain); got != "com.example.environments" {
		t.Errorf("Expected the provider's bundle id as the default, got %q", got)
	}
	if got := common.BundleIDForDeliveryPoint(psp, nil); got != "com.example.environments" {
		t.Errorf("Expected the provider's bundle id with no device, got %q", got)
	}
	if got := common.BundleIDForDeliveryPoint(nil, device); got != "com.example.app.enterprise" {
		t.Errorf("Expected the device's bundle id with no provider, got %q", got)
	}
}
