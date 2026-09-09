package http_api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests for which apns-topic a push carries.
//
// One APNs certificate can be valid for several bundle ids, while a provider
// holds one and a service holds one provider. HTTP/2 requires the header, and
// the binary protocol that did not send it is gone, so #181's workaround --
// uniqush.http2=0 -- has not worked since Apple shut that protocol down.

// deliveryPointWithBundleID builds a device that names its own app.
func deliveryPointWithBundleID(t *testing.T, subscriber, bundleid string) *push.DeliveryPoint {
	t.Helper()
	dp := push.NewEmptyDeliveryPoint()
	dp.FixedData["subscriber"] = subscriber
	dp.FixedData["devtoken"] = "0123456789abcdef"
	if bundleid != "" {
		dp.VolatileData["bundleid"] = bundleid
	}
	return dp
}

// topicsFromPush runs one push for the given delivery points and returns how
// many requests carried each apns-topic, alongside anything reported as an
// error.
//
// Counted rather than listed in order: a batch is sent one goroutine per
// device, so the order requests arrive in is the scheduler's business and an
// assertion about it would fail for reasons having nothing to do with headers.
func topicsFromPush(t *testing.T, psp *push.PushServiceProvider, dps []*push.DeliveryPoint) (map[string]int, []push.Error) {
	t.Helper()

	processor := newHTTPRequestProcessor()
	var lock sync.Mutex
	topics := make(map[string]int)
	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		topic := ""
		//nolint:staticcheck // SA1008: HTTP/2 field names are lowercase on the wire
		if values := r.Header["apns-topic"]; len(values) > 0 {
			topic = values[0]
		}
		lock.Lock()
		topics[topic]++
		lock.Unlock()
		body := newMockResponse([]byte{}, r)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, body, nil
	})

	tokens := make([][]byte, len(dps))
	for i := range tokens {
		tokens[i] = devToken
	}
	errChan := make(chan push.Error, 8)
	processor.AddRequest(&common.PushRequest{
		PSP:       psp,
		Devtokens: tokens,
		DPList:    dps,
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, 8),
	})

	var errs []push.Error
	for err := range errChan {
		errs = append(errs, err)
	}
	return topics, errs
}

// TestDeviceBundleIDOverridesTheProviders is #181.
//
// Two builds of one app, one certificate, one service. Before this they needed
// a service each, and every device had to subscribe to the right one.
func TestDeviceBundleIDOverridesTheProviders(t *testing.T) {
	release := deliveryPointWithBundleID(t, "alice", "com.example.app")
	enterprise := deliveryPointWithBundleID(t, "bob", "com.example.app.enterprise")

	topics, errs := topicsFromPush(t, pushServiceProvider, []*push.DeliveryPoint{release, enterprise})
	if len(errs) != 0 {
		t.Fatalf("Expected no errors, got %v", errs)
	}
	if topics["com.example.app"] != 1 || topics["com.example.app.enterprise"] != 1 {
		t.Errorf("Expected one push under each device's own bundle id, got %v", topics)
	}
}

// TestProviderBundleIDIsTheDefault is the compatibility case, and every
// installation that exists today.
//
// No device names a bundle id, so every push carries the provider's, exactly
// as it did when the header was built once for the batch.
func TestProviderBundleIDIsTheDefault(t *testing.T) {
	first := deliveryPointWithBundleID(t, "alice", "")
	second := deliveryPointWithBundleID(t, "bob", "")

	topics, errs := topicsFromPush(t, pushServiceProvider, []*push.DeliveryPoint{first, second})
	if len(errs) != 0 {
		t.Fatalf("Expected no errors, got %v", errs)
	}
	if topics[bundleID] != 2 {
		t.Errorf("Expected both pushes to carry the provider's bundle id %q, got %v", bundleID, topics)
	}
}

// TestAProviderWithNoBundleIDStillPushes covers what used to be refused
// outright.
//
// The check was per provider: a provider with no bundleid failed every device
// in the push. It is per device now, so a provider that names none is
// serviceable as long as its devices do -- which is the arrangement #181 wants
// for a certificate shared between builds.
func TestAProviderWithNoBundleIDStillPushes(t *testing.T) {
	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         mockServiceName,
		"pushservicetype": "apns",
		"cert":            "../apns-test/localhost.cert",
		"key":             "../apns-test/localhost.key",
		"skipverify":      "true",
	})
	if err != nil {
		t.Fatalf("Could not build a provider without a bundle id: %v", err)
	}

	named := deliveryPointWithBundleID(t, "alice", "com.example.app")
	anonymous := deliveryPointWithBundleID(t, "bob", "")

	topics, errs := topicsFromPush(t, psp, []*push.DeliveryPoint{named, anonymous})

	// The device that named its app is pushed to.
	if topics["com.example.app"] != 1 {
		t.Errorf("Expected one push carrying com.example.app, got %v", topics)
	}
	// The device that named none has nowhere to go, and is the only one
	// refused. Refusing the whole push, which is what the provider-wide check
	// did, would have refused the device that was perfectly serviceable.
	if len(errs) != 1 {
		t.Fatalf("Expected exactly one error, got %d: %v", len(errs), errs)
	}
	if got := push.DestinationOf(errs[0]); got != anonymous {
		t.Errorf("The refusal named %v rather than the device with no bundle id", got)
	}
}
