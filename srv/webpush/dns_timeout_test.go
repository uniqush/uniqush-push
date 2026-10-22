package webpush

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/push"
)

func TestRequestTimeoutIncludesEndpointDNS(t *testing.T) {
	for _, name := range []string{"webpush", "unifiedpush"} {
		t.Run(name, func(t *testing.T) {
			service := NewPushService(name).(*pushService)
			service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("A push must not be sent when endpoint DNS times out")
				return newResponse(201, nil), nil
			})
			file := conf.NewConfigFile()
			file.AddOption(name, "request_timeout", "1")
			service.SetPushServiceConfig(push.NewPushServiceConfig(file, name))
			policy := NewEndpointPolicy()
			policy.resolve = func(ctx context.Context, _, _ string) ([]net.IP, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			service.policy.Store(policy)
			psp := newTestPSP(t, service)
			dp := newTestDP(t, service)
			dp.FixedData["endpoint"] = "https://slow.example/up"

			started := time.Now()
			result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "x"}})
			elapsed := time.Since(started)
			failure, ok := result.Err.(*push.BadDeliveryPoint)
			if !ok {
				t.Fatalf("Expected an endpoint validation error, got %T", result.Err)
			}
			if !strings.Contains(failure.Details, context.DeadlineExceeded.Error()) {
				t.Errorf("Expected endpoint DNS to hit request_timeout, got %s", failure.Details)
			}
			if elapsed >= 2*time.Second {
				t.Errorf("Endpoint DNS outlived the one-second request_timeout: %v", elapsed)
			}
		})
	}
}
