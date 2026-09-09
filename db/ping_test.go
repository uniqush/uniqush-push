package db

import (
	"net"
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

// closedPort returns a port on localhost that nothing is listening on.
//
// Found by opening a listener, asking the kernel which port it got, and closing
// it again -- rather than by naming a port that ought to be free. A listening
// socket that never accepted a connection is released immediately, so a connect
// to it is refused.
//
// Not airtight: something could bind that port between the close here and the
// dial below. It is a far smaller window than assuming a particular number is
// unused, and if it ever loses the race the test fails by connecting to
// something that is not redis, which does not pass either.
func closedPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Could not open a listener to find a free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("Could not close the listener: %v", err)
	}
	return port
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
	config.Host = "127.0.0.1"
	config.Port = closedPort(t)

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
