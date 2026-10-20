package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
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
	// The probe is for this host's server, never one reached via a proxy.
	transport := &http.Transport{Proxy: nil}
	var url string
	if path, ok := strings.CutPrefix(addr, "unix:"); ok {
		if runtime.GOOS == "windows" {
			return errors.New("unix: addresses are not supported on Windows")
		}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
		url = "http://uniqush" + HealthURL
	} else {
		var host, port string
		if host, port, err = net.SplitHostPort(addr); err != nil {
			return err
		}
		// A server listening on all interfaces is reachable on loopback, and the
		// unspecified address is not something to connect to.
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			host = "127.0.0.1"
		}
		url = "http://" + net.JoinHostPort(host, port) + HealthURL
	}
	client := http.Client{Transport: transport, Timeout: healthcheckTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", HealthURL, resp.Status)
	}
	return nil
}
