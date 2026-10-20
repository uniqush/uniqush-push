package main

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// healthcheckTimeout is short because Docker runs the check on an interval and
// a hung uniqush should count as unhealthy, not as a check still running.
const healthcheckTimeout = 3 * time.Second

// healthcheck asks the running uniqush for /health on the address its config
// listens on. It exists because the Docker image is built FROM scratch and has
// no curl or wget to do it with.
func healthcheck(confPath string) error {
	c, err := OpenConfig(confPath)
	if err != nil {
		return err
	}
	addr, err := LoadRestAddr(c)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	// A server listening on all interfaces is reachable on loopback, and the
	// unspecified address is not something to connect to.
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + HealthURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", HealthURL, resp.Status)
	}
	return nil
}
