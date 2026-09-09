package main

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"
)

// TestStartupRefusesWhenTheSystemRootsCannotBeLoaded is the point of #257.
//
// crypto/x509 caches the outcome of loading the store for the life of the
// process, so a failure at the first handshake is permanent and every push
// after it fails the same way. A server in that state starts cleanly and
// reports itself healthy while delivering nothing, which is the failure mode
// this converts into one line at startup.
func TestStartupRefusesWhenTheSystemRootsCannotBeLoaded(t *testing.T) {
	previous := systemCertPool
	t.Cleanup(func() { systemCertPool = previous })

	systemCertPool = func() (*x509.CertPool, error) {
		return nil, errors.New("open /etc/ssl/certs: no such file or directory")
	}

	err := checkSystemRoots()
	if err == nil {
		t.Fatal("Expected uniqush to refuse to start with no system root certificates")
	}
	// The operator reading this is looking at a server that will not start, so
	// the message has to carry both what is wrong and what to do about it.
	for _, expected := range []string{"no such file or directory", "ca-certificates", "SSL_CERT_FILE"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Expected the error to mention %q, got: %v", expected, err)
		}
	}
}

// TestStartupRefusesWhenTheSystemRootsAreEmpty covers the other shape of the
// same answer.
//
// x509.SystemCertPool is documented to return an error rather than a nil pool,
// but nothing in the type system says so, and a nil pool would otherwise be
// handed to crypto/tls as "verify against nothing".
func TestStartupRefusesWhenTheSystemRootsAreEmpty(t *testing.T) {
	previous := systemCertPool
	t.Cleanup(func() { systemCertPool = previous })

	systemCertPool = func() (*x509.CertPool, error) { return nil, nil }

	if err := checkSystemRoots(); err == nil {
		t.Error("Expected an empty system certificate store to stop uniqush starting")
	}
}

// TestStartupAcceptsARealSystemCertPool checks the check does not fire on a
// machine that is fine, which is every machine uniqush normally runs on.
//
// Against the real x509.SystemCertPool rather than a stub, because a check that
// refuses to start is only as good as its false positive rate: this is the
// assertion that CI, a container image and a developer laptop all pass it.
func TestStartupAcceptsARealSystemCertPool(t *testing.T) {
	if err := checkSystemRoots(); err != nil {
		t.Errorf("uniqush refused to start on a machine with a working certificate store: %v", err)
	}
}
