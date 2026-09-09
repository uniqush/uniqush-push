package db

import (
	"testing"
)

// TestPingReachesARunningRedis is the half of the health check that talks to a
// real server.
//
// The REST tests drive the handler against a stub, which proves what uniqush
// answers and not that the question means anything. This runs against the redis
// the rest of the package's tests use, and is skipped along with them when there
// is none.
func TestPingReachesARunningRedis(t *testing.T) {
	client := connectDatabaseAndClearRedisData(t)
	if err := client.Ping(); err != nil {
		t.Errorf("Ping failed against a redis every other test in this package is using: %v", err)
	}
}

// TestPingFailsWhenRedisIsNotThere covers the answer the endpoint exists to
// give.
//
// Against a port nothing is listening on, rather than by stubbing the client:
// what is being checked is that a connection failure comes back as an error
// rather than as a hang or a nil, and that is a property of the redis client
// and its timeouts.
func TestPingFailsWhenRedisIsNotThere(t *testing.T) {
	config := getTestDatabaseConfig()
	// Port 1 is reserved, so nothing legitimate is listening there.
	config.Port = 1

	client, err := NewPushDatabaseWithoutCache(config)
	if err != nil {
		// Connecting is lazy in go-redis, so this is not expected -- but if a
		// future client dials eagerly, refusing to construct is an equally good
		// answer to "is redis reachable".
		t.Skipf("The database refused to be constructed, which answers the question early: %v", err)
	}

	if err := client.Ping(); err == nil {
		t.Error("Ping succeeded against a port nothing is listening on")
	}
}
