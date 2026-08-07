package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestIsErrCausedByMissingKey covers the predicate directly.
//
// This is load-bearing and easy to break silently. pushdb.go uses it to decide
// whether a delivery point referenced by a subscription set but absent from the
// database should be garbage-collected. If it starts returning false for a real
// missing key, orphaned delivery points accumulate forever and every call to
// /subscriptions for the affected subscriber fails instead of self-healing.
func TestIsErrCausedByMissingKey(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "the bare sentinel", err: redis.Nil, expected: true},
		{
			// This is the case that matters: pushredisdb.go never returns the
			// sentinel unwrapped, so if the wrapping loses it the predicate is
			// useless. It must be %w, not %v.
			name:     "wrapped once with %w",
			err:      fmt.Errorf("GetDeliveryPoint failed: %w", redis.Nil),
			expected: true,
		},
		{
			name:     "wrapped twice with %w",
			err:      fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", redis.Nil)),
			expected: true,
		},
		{
			// %v flattens the error to text, so the sentinel is gone even though
			// the message still reads "redis: nil". The old implementation
			// string-matched and therefore accepted this; errors.Is does not.
			// Any call site that formats with %v is a bug.
			name:     "flattened with %v is not recognised",
			err:      fmt.Errorf("GetDeliveryPoint failed: %v", redis.Nil), //nolint:errorlint // that is the point
			expected: false,
		},
		{name: "an unrelated error", err: errors.New("connection refused"), expected: false},
		{
			// The previous implementation matched any error whose text merely
			// contained the phrase.
			name:     "an unrelated error that mentions the phrase",
			err:      errors.New("could not parse config value \"redis: nil\""),
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isErrCausedByMissingKey(testCase.err); got != testCase.expected {
				t.Errorf("isErrCausedByMissingKey(%v) = %v, expected %v", testCase.err, got, testCase.expected)
			}
		})
	}
}

// TestMissingKeyErrorsSurviveTheDatabaseLayer is the end-to-end version, run
// against a real redis. It asserts that the errors the database actually
// produces for absent keys are still recognisable as missing-key errors after
// whatever wrapping the accessors apply.
//
// A unit test on the predicate alone would not have caught the %v/%w bug,
// because the bug was in the caller's format verb rather than in the predicate.
func TestMissingKeyErrorsSurviveTheDatabaseLayer(t *testing.T) {
	database := connectDatabaseAndClearRedisData(t)
	redisDB := database.(*pushDatabaseOpts).db.(*PushRedisDB)

	t.Run("GetDeliveryPoint", func(t *testing.T) {
		_, err := redisDB.GetDeliveryPoint("definitely-not-a-real-delivery-point")
		if err == nil {
			t.Fatal("Expected an error for a missing delivery point")
		}
		if !isErrCausedByMissingKey(err) {
			t.Errorf("A missing delivery point must be recognisable as a missing key, got: %v", err)
		}
	})

	t.Run("GetPushServiceProvider", func(t *testing.T) {
		_, err := redisDB.GetPushServiceProvider("definitely-not-a-real-psp")
		if err == nil {
			t.Fatal("Expected an error for a missing push service provider")
		}
		if !isErrCausedByMissingKey(err) {
			t.Errorf("A missing psp must be recognisable as a missing key, got: %v", err)
		}
	})

	t.Run("GetPushServiceProviderNameByServiceDeliveryPoint", func(t *testing.T) {
		_, err := redisDB.GetPushServiceProviderNameByServiceDeliveryPoint("nosuchservice", "nosuchdp")
		if err == nil {
			t.Fatal("Expected an error for a missing service/delivery point mapping")
		}
		if !isErrCausedByMissingKey(err) {
			t.Errorf("A missing mapping must be recognisable as a missing key, got: %v", err)
		}
	})
}

// TestMGetHandlesMissingKeys pins the convention mgetStrings depends on: under
// go-redis, MGet returns a nil interface element for an absent key and a string
// for a present one. This is unchanged from v6 to v9 and under both RESP2 and
// RESP3, but the type switch in mgetStrings would break loudly if it ever
// changed, and this says so out loud.
func TestMGetHandlesMissingKeys(t *testing.T) {
	database := connectDatabaseAndClearRedisData(t)
	redisDB := database.(*pushDatabaseOpts).db.(*PushRedisDB)

	present := DeliveryPointPrefix + "present"
	if err := redisDB.client.Set(redisDB.ctx, present, "value", 0).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	results, err := redisDB.mgetStrings(present, DeliveryPointPrefix+"absent")
	if err != nil {
		t.Fatalf("mgetStrings returned an error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}
	if string(results[0]) != "value" {
		t.Errorf("Expected the present key to return its value, got %q", results[0])
	}
	if results[1] != nil {
		t.Errorf("Expected the absent key to return nil, got %q", results[1])
	}
}

// TestExistsReturnsACount guards the other silent-breakage candidate. Redis
// EXISTS returns a count, and go-redis surfaces it as *IntCmd; code that
// expected a bool would compile against int64 comparisons in surprising ways.
func TestExistsReturnsACount(t *testing.T) {
	database := connectDatabaseAndClearRedisData(t)
	redisDB := database.(*pushDatabaseOpts).db.(*PushRedisDB)

	key := ServiceToPushServiceProvidersPrefix + "existstest"
	if got, err := redisDB.client.Exists(redisDB.ctx, key).Result(); err != nil || got != 0 {
		t.Errorf("Expected 0 for an absent key, got %d (err %v)", got, err)
	}
	if err := redisDB.client.SAdd(redisDB.ctx, key, "member").Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}
	if got, err := redisDB.client.Exists(redisDB.ctx, key).Result(); err != nil || got != 1 {
		t.Errorf("Expected 1 for a present key, got %d (err %v)", got, err)
	}
}
