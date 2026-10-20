package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HealthURL {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ok.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedAddr := closed.Listener.Addr().String()
	closed.Close()

	_, okPort, _ := net.SplitHostPort(ok.Listener.Addr().String())

	// Not t.TempDir(): its path can exceed the ~104-byte limit on a socket's.
	sockDir, err := os.MkdirTemp("", "uniqush")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "api.sock")
	unixLn, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	onUnix := httptest.NewUnstartedServer(ok.Config.Handler)
	onUnix.Listener = unixLn
	onUnix.Start()
	defer onUnix.Close()

	cases := []struct {
		name    string
		addr    string
		healthy bool
	}{
		{"healthy", ok.Listener.Addr().String(), true},
		{"listening on all interfaces", "0.0.0.0:" + okPort, true},
		{"empty host", ":" + okPort, true},
		{"unix socket", "unix:" + sockPath, runtime.GOOS != "windows"},
		{"unhealthy", unhealthy.Listener.Addr().String(), false},
		{"not listening", closedAddr, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			confPath := filepath.Join(t.TempDir(), "uniqush-push.conf")
			if err := os.WriteFile(confPath, []byte("[WebFrontend]\naddr="+c.addr+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := healthcheck(confPath)
			if c.healthy && err != nil {
				t.Errorf("Expected healthy, got %v", err)
			}
			if !c.healthy && err == nil {
				t.Error("Expected an error, got none")
			}
		})
	}
}
