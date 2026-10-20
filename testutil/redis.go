package testutil

import (
	"net"
	"os"
	"strconv"
)

// RedisAddr is the redis the tests use, and FLUSH: UNIQUSH_TEST_REDIS_ADDR
// (host:port), or localhost:6379. Set it to keep the tests off a redis holding
// data you want.
func RedisAddr() (host string, port int) {
	addr := os.Getenv("UNIQUSH_TEST_REDIS_ADDR")
	if addr == "" {
		return "localhost", 6379
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		panic("UNIQUSH_TEST_REDIS_ADDR is not host:port: " + err.Error())
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		panic("UNIQUSH_TEST_REDIS_ADDR has a bad port: " + err.Error())
	}
	return host, port
}
