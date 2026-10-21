package webpush

import (
	"context"
	"errors"
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
			// No packets leave the test. The watchdog releases the old,
			// unbounded lookup so a regression fails instead of hanging.
			release := make(chan struct{})
			watchdog := time.AfterFunc(3*time.Second, func() { close(release) })
			defer watchdog.Stop()
			previous := net.DefaultResolver
			net.DefaultResolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return nil, errors.New("test DNS watchdog fired")
					}
				},
			}
			t.Cleanup(func() { net.DefaultResolver = previous })

			service := NewPushService(name).(*pushService)
			service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("A push must not be sent when endpoint DNS times out")
				return newResponse(201, nil), nil
			})
			file := conf.NewConfigFile()
			file.AddOption(name, "request_timeout", "1")
			service.SetPushServiceConfig(push.NewPushServiceConfig(file, name))
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
			// net.Resolver reports an expired context as a DNS i/o timeout.
			if !strings.Contains(failure.Details, "i/o timeout") &&
				!strings.Contains(failure.Details, context.DeadlineExceeded.Error()) {
				t.Errorf("Expected endpoint DNS to hit request_timeout, got %s", failure.Details)
			}
			if elapsed >= 2*time.Second {
				t.Errorf("Endpoint DNS outlived the one-second request_timeout: %v", elapsed)
			}
		})
	}
}

func TestRequestTimeoutSharedWithEndpointDNS(t *testing.T) {
	for _, name := range []string{"webpush", "unifiedpush"} {
		t.Run(name, func(t *testing.T) {
			service := NewPushService(name).(*pushService)
			var lookupContext context.Context
			policy := NewEndpointPolicy()
			policy.resolve = func(ctx context.Context, _, _ string) ([]net.IP, error) {
				lookupContext = ctx
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			service.policy.Store(policy)
			seen := false
			service.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Body != nil {
					defer request.Body.Close()
				}
				seen = true
				if lookupContext == nil {
					t.Error("The endpoint was sent without DNS validation")
				} else {
					dnsDeadline, dnsBounded := lookupContext.Deadline()
					httpDeadline, httpBounded := request.Context().Deadline()
					if !dnsBounded || !httpBounded || !dnsDeadline.Equal(httpDeadline) {
						t.Errorf("DNS and HTTP must share one deadline: DNS %v, HTTP %v", dnsDeadline, httpDeadline)
					}
				}
				return newResponse(201, nil), nil
			})
			psp := newTestPSP(t, service)
			dp := newTestDP(t, service)
			dp.FixedData["endpoint"] = "https://public.example/up"
			result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "x"}})
			if result.Err != nil {
				t.Fatalf("Expected a successful push, got %T", result.Err)
			}
			if !seen {
				t.Error("No HTTP request was sent")
			}
			if lookupContext != nil && !errors.Is(lookupContext.Err(), context.Canceled) {
				t.Error("The shared timeout context was not released after the push")
			}
		})
	}
}
