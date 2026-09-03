package main

import (
	"context"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// The acceptance test for unbinding delivery points from a provider's
// credential hash: the case that motivated the whole branch, end to end.
//
// Everything below it is a unit of that story -- the db package proves the
// delivery points survive, srv/apns proves a .p8 provider can authenticate --
// but neither proves the two compose. This one subscribes devices to a
// certificate-based APNs service, replaces the provider with a signing key, and
// pushes to the same devices, through the real backend against the simulator.
// No Apple account, and no re-subscribe.

const (
	acceptanceService    = "rebinding_acceptance_service"
	acceptanceSubscriber = "rebinding_acceptance_subscriber"
	acceptanceBundleID   = "com.example.rebinding"
)

// installAPNSOnce guards the push service manager, which is a process-wide
// singleton that refuses a second registration of the same type.
var installAPNSOnce sync.Once

// acceptanceRedisDB is the redis database index this test flushes and uses.
//
// Not 0, which is where the db package's own tests live. `go test ./...` builds
// one binary per package and runs them in parallel, so with both on 0 either
// package would periodically flush the other's fixtures out from under it --
// a failure that appears only under load and blames whichever test happened to
// be reading at the time.
const acceptanceRedisDB = 1

// connectAcceptanceDatabase opens the test redis and clears it.
//
// Skips rather than fails when there is no redis, matching the db package's
// own tests: CI runs a redis service container, and a developer without one
// should not see a failure they cannot act on.
func connectAcceptanceDatabase(t *testing.T, psm *push.PushServiceManager) db.PushDatabase {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: acceptanceRedisDB, Protocol: 2})
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		t.Skipf("No redis on localhost:6379: %v", err)
	}
	// FLUSHDB, not FLUSHALL: it empties the database this client selected and
	// leaves the others, which is the whole point of being on a separate one.
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		client.Close()
		t.Skipf("Could not clear redis: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	database, err := db.NewPushDatabaseWithoutCache(&db.DatabaseConfig{
		Engine:             "redis",
		Host:               "localhost",
		Port:               6379,
		Name:               strconv.Itoa(acceptanceRedisDB),
		PushServiceManager: psm,
	})
	if err != nil {
		t.Fatalf("Could not open the test database: %v", err)
	}
	return database
}

// countingHandler records what the push reported, so the test can tell a push
// that failed quietly from one that never happened.
type countingHandler struct {
	mutex   sync.Mutex
	details []APIResponseDetails
}

func (h *countingHandler) AddDetailsToHandler(v APIResponseDetails) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.details = append(h.details, v)
}

func (h *countingHandler) ToJSON() []byte { return nil }

func (h *countingHandler) errors() []APIResponseDetails {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	var failures []APIResponseDetails
	for _, detail := range h.details {
		if detail.Code != UNIQUSH_SUCCESS {
			failures = append(failures, detail)
		}
	}
	return failures
}

func TestReplacingAPNsCredentialsKeepsDevicesReachable(t *testing.T) {
	installAPNSOnce.Do(srv.InstallAPNS)
	psm := push.GetPushServiceManager()

	// The simulator is not an Apple host, and pointing a provider at one is
	// gated on a config option so that it cannot happen by accident. Set it
	// directly rather than through a config file: this test is about the
	// migration, and srv/apns already covers the gate itself.
	common.SetAllowNonAppleEndpoints(true)
	t.Cleanup(func() { common.SetAllowNonAppleEndpoints(false) })

	database := connectAcceptanceDatabase(t, psm)
	backend := NewPushBackEnd(psm, database, silentLoggers())

	server := apnstest.NewServer()
	defer server.Close()
	caCert, err := server.WriteCACert(t.TempDir())
	if err != nil {
		t.Fatalf("Could not write the simulator's CA certificate: %v", err)
	}

	// A certificate-based provider, as an existing deployment would have.
	certificatePSP, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"service":          acceptanceService,
		"pushservicetype":  "apns",
		"cert":             "srv/apns/apns-test/localhost.cert",
		"key":              "srv/apns/apns-test/localhost.key",
		"bundleid":         acceptanceBundleID,
		common.EndpointKey: server.URL(),
		common.CACertKey:   caCert,
	})
	if err != nil {
		t.Fatalf("Could not build the certificate provider: %v", err)
	}
	if err = backend.AddPushServiceProvider(acceptanceService, certificatePSP, false); err != nil {
		t.Fatalf("Could not add the certificate provider: %v", err)
	}

	// Two devices subscribe to it.
	devtokens := []string{
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
	}
	for _, devtoken := range devtokens {
		dp, e := psm.BuildDeliveryPointFromMap(map[string]string{
			"service":         acceptanceService,
			"subscriber":      acceptanceSubscriber,
			"pushservicetype": "apns",
			"devtoken":        devtoken,
		})
		if e != nil {
			t.Fatalf("Could not build a delivery point: %v", e)
		}
		if _, e = database.AddDeliveryPointToService(acceptanceService, acceptanceSubscriber, dp); e != nil {
			t.Fatalf("Could not subscribe %s: %v", devtoken, e)
		}
	}

	// The migration: a .p8 signing key for the same service. Its fixed data
	// differs from the certificate provider's -- a signing key has to be
	// rotatable, so it cannot be part of a provider's identity -- which is
	// exactly why this used to be refused.
	key, err := apnstest.GenerateSigningKey(t.TempDir(), "KEYID12345", "TEAMID6789")
	if err != nil {
		t.Fatalf("Could not generate a signing key: %v", err)
	}
	server.RequireToken(key)

	tokenPSP, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"service":          acceptanceService,
		"pushservicetype":  "apns",
		"bundleid":         acceptanceBundleID,
		common.AuthKeyKey:  key.Path,
		common.KeyIDKey:    key.KeyID,
		common.TeamIDKey:   key.TeamID,
		common.EndpointKey: server.URL(),
		common.CACertKey:   caCert,
	})
	if err != nil {
		t.Fatalf("Could not build the token provider: %v", err)
	}
	if tokenPSP.Name() == certificatePSP.Name() {
		t.Fatal("The two providers hash the same, so this test would prove nothing")
	}

	// Without replace=true this is still refused, which is the protection
	// against pasting a credential into the wrong service.
	if err = backend.AddPushServiceProvider(acceptanceService, tokenPSP, false); err == nil {
		t.Fatal("Expected a conflicting provider to be refused without replace=true")
	}

	if err = backend.AddPushServiceProvider(acceptanceService, tokenPSP, true); err != nil {
		t.Fatalf("Could not replace the certificate provider: %v", err)
	}

	// Nobody re-subscribed. Push to the same subscriber.
	handler := &countingHandler{}
	notification := &push.Notification{Data: map[string]string{
		"msg":   "the credentials changed underneath you",
		"badge": "1",
	}}
	backend.Push("acceptance-request", "127.0.0.1", acceptanceService,
		[]string{acceptanceSubscriber}, nil, notification, nil,
		log.NewLogger(io.Discard, "[test]", log.LOGLEVEL_SILENT), handler)

	if failures := handler.errors(); len(failures) > 0 {
		for _, failure := range failures {
			t.Errorf("Push reported code %v: %v", failure.Code, stringOrEmpty(failure.ErrorMsg))
		}
		t.Fatal("The push should have succeeded for every device")
	}

	// The simulator is the witness: both devices were pushed to, and the
	// requests carried a provider token rather than arriving unauthenticated.
	requests := server.Requests()
	if len(requests) != len(devtokens) {
		t.Fatalf("Expected %d pushes, the simulator saw %d", len(devtokens), len(requests))
	}
	pushed := make(map[string]bool, len(requests))
	for _, request := range requests {
		pushed[request.Token] = true
		if request.Header.Get("authorization") == "" {
			t.Errorf("Push to %s carried no provider token", request.Token)
		}
		if request.Topic != acceptanceBundleID {
			t.Errorf("Push to %s carried topic %q, expected %q", request.Token, request.Topic, acceptanceBundleID)
		}
	}
	for _, devtoken := range devtokens {
		if !pushed[devtoken] {
			t.Errorf("Device %s was not pushed to, so it lost its subscription", devtoken)
		}
	}
	if violations := server.Violations(); len(violations) > 0 {
		t.Errorf("The simulator reported protocol violations: %v", violations)
	}

	// And the old provider is gone rather than lingering as a second provider
	// of the same push service type.
	report, err := database.CheckConsistency()
	if err != nil {
		t.Fatalf("CheckConsistency failed: %v", err)
	}
	counts := report.CountByKind()
	// One stale binding per device is the expected residue, not a defect: the
	// bindings still name the provider that was replaced, and they stay that
	// way until the index stops being written. This is the count the plan says
	// to watch fall to zero when that happens, so it is asserted exactly rather
	// than waved through.
	if counts[db.ProblemStaleBinding] != len(devtokens) {
		t.Errorf("Expected %d stale bindings after the migration, got %d", len(devtokens), counts[db.ProblemStaleBinding])
	}
	delete(counts, db.ProblemStaleBinding)
	if len(counts) != 0 {
		t.Errorf("Expected nothing but stale bindings after the migration, got: %v", report.Problems)
	}
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Unused elsewhere, but a compile-time check that the handler above satisfies
// the interface the backend expects.
var _ APIResponseHandler = &countingHandler{}
