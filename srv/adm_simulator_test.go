package srv

import (
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/admtest"
)

func TestADMWithSimulator(t *testing.T) {
	cases := []struct {
		name     string
		response admtest.Response
		result   string
		retry    time.Duration
	}{
		{"success", admtest.Response{Status: 200}, "success", 0},
		{"invalid registration", admtest.Response{Status: 400, Reason: "InvalidRegistrationId"}, "bad destination", 0},
		{"unregistered", admtest.Response{Status: 400, Reason: "Unregistered"}, "error", 0},
		{"bad payload", admtest.Response{Status: 400, Reason: "InvalidData"}, "error", 0},
		{"large payload", admtest.Response{Status: 413, Reason: "MessageTooLarge"}, "bad notification", 0},
		{"expired token", admtest.Response{Status: 401, Reason: "AccessTokenExpired"}, "retry", 10 * time.Second},
		{"throttled", admtest.Response{Status: 429, RetryAfter: "7"}, "retry", 7 * time.Second},
		{"unavailable", admtest.Response{Status: 503, RetryAfter: "invalid"}, "retry", time.Minute},
		{"server error", admtest.Response{Status: 500}, "retry", time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := admtest.NewServer()
			defer server.Close()
			server.SetResponse("test-registration", c.response)
			client := server.Client()
			defer client.CloseIdleConnections()
			service := newADMPushService(client)
			defer close(service.pspLock)
			manager := new(push.PushServiceManager)
			manager.ClearAllPushServiceTypesForUnitTest()
			if err := manager.RegisterPushServiceType(service); err != nil {
				t.Fatal(err)
			}
			provider, err := manager.BuildPushServiceProviderFromMap(map[string]string{
				"pushservicetype": "adm", "service": "test", "clientid": "test-client", "clientsecret": "test-secret",
			})
			if err != nil {
				t.Fatal(err)
			}
			destination, err := manager.BuildDeliveryPointFromMap(map[string]string{
				"pushservicetype": "adm", "service": "test", "subscriber": "test", "regid": "test-registration",
			})
			if err != nil {
				t.Fatal(err)
			}
			// The second push must reuse the token, while still producing its own result.
			for attempt := range 2 {
				queue := make(chan *push.DeliveryPoint, 1)
				queue <- destination
				close(queue)
				results := make(chan *push.Result)
				go service.Push(provider, queue, results, &push.Notification{Data: map[string]string{"message": "hello"}})
				updates, deliveries := 0, 0
				for result := range results {
					if _, ok := result.Err.(*push.PushServiceProviderUpdate); ok {
						updates++
						continue
					}
					deliveries++
					if result.Destination != destination {
						t.Error("result lost its destination")
					}
					var kind string
					switch e := result.Err.(type) {
					case nil:
						kind = "success"
						if result.MsgID != "test-request" {
							t.Errorf("message ID = %q", result.MsgID)
						}
					case *push.BadDeliveryPoint:
						kind = "bad destination"
					case *push.BadNotification:
						kind = "bad notification"
					case *push.RetryError:
						kind = "retry"
						if e.After != c.retry {
							t.Errorf("retry after = %s, want %s", e.After, c.retry)
						}
					case *push.ErrorReport:
						kind = "error"
					case *push.UnsubscribeUpdate:
						kind = "unsubscribe"
					case *push.InvalidRegistrationUpdate:
						kind = "invalid registration"
					default:
						kind = "unknown"
					}
					if kind != c.result {
						t.Errorf("result = %s (%v), want %s", kind, result.Err, c.result)
					}
				}
				if deliveries != 1 || updates != 1-attempt {
					t.Errorf("deliveries=%d updates=%d on attempt %d", deliveries, updates, attempt)
				}
			}
			if tokens, messages := server.Counts(); tokens != 1 || messages != 2 {
				t.Errorf("token exchanges=%d messages=%d", tokens, messages)
			}
		})
	}
}
