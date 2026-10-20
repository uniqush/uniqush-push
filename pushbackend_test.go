package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
)

// A notification the backend refuses fails before any device is tried, and the
// failure still says whose push it was.
func TestRefusedNotificationNamesTheRequest(t *testing.T) {
	backend, _ := setUpRemovalTest(t)
	const service = "refused_notification"
	pspKV, dpKV := webPushFixture(t, service, webPushServer(t, http.StatusCreated).URL+"/sub/1")
	dp := subscribeForRemoval(t, backend, pspKV, dpKV)

	cases := []struct {
		name          string
		ids           []string
		deliveryPoint string
	}{
		{"by subscriber", nil, "Unknown"},
		{"by delivery_point_id", []string{dp.Name()}, dp.Name()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logger := log.NewLogger(io.Discard, "[test]", log.LevelSilent)
			handler := newPushResponseHandler(logger)
			backend.Push("refused-request", "127.0.0.1", service, []string{removalSubscriber}, c.ids,
				&push.Notification{Data: map[string]string{"msg": "hello", "ttl": "1h"}}, nil, logger, handler)
			var response APIPushResponse
			if err := json.Unmarshal(handler.ToJSON(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.FailureDetails) != 1 {
				t.Fatalf("Expected one failure, got %+v", response)
			}
			got := response.FailureDetails[0]
			if stringOrEmpty(got.Subscriber) != removalSubscriber || stringOrEmpty(got.DeliveryPoint) != c.deliveryPoint {
				t.Errorf("Expected subscriber %q and deliveryPoint %q, got %q and %q",
					removalSubscriber, c.deliveryPoint, stringOrEmpty(got.Subscriber), stringOrEmpty(got.DeliveryPoint))
			}
		})
	}
}
