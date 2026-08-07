package webpush

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestValidateSyntax(t *testing.T) {
	testCases := []struct {
		name        string
		endpoint    string
		expectError string
	}{
		{name: "https is fine", endpoint: "https://ntfy.sh/up?id=abc"},
		{name: "http is allowed", endpoint: "http://push.example.org/x"},
		{name: "uppercase scheme is fine", endpoint: "HTTPS://ntfy.sh/up"},
		{name: "empty", endpoint: "", expectError: "empty"},
		{name: "no host", endpoint: "https:///path", expectError: "no host"},
		{name: "ftp rejected", endpoint: "ftp://example.org/x", expectError: "not allowed"},
		{name: "file rejected", endpoint: "file:///etc/passwd", expectError: "not allowed"},
		{name: "gopher rejected", endpoint: "gopher://example.org", expectError: "not allowed"},
		{
			// The UnifiedPush spec caps endpoints at 1000 bytes.
			name:        "over 1000 bytes",
			endpoint:    "https://example.org/" + strings.Repeat("a", 1000),
			expectError: "maximum",
		},
	}

	policy := NewEndpointPolicy()
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := policy.ValidateSyntax(testCase.endpoint)
			if testCase.expectError == "" {
				if err != nil {
					t.Fatalf("Expected acceptance, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Expected rejection mentioning %q", testCase.expectError)
			}
			if !strings.Contains(err.Error(), testCase.expectError) {
				t.Errorf("Expected error mentioning %q, got: %v", testCase.expectError, err)
			}
		})
	}
}

func TestAllowedHosts(t *testing.T) {
	policy := NewEndpointPolicy()
	policy.SetAllowedHosts([]string{"ntfy.sh"})

	if err := policy.ValidateSyntax("https://ntfy.sh/up"); err != nil {
		t.Errorf("Expected an allow-listed host to pass, got: %v", err)
	}
	if err := policy.ValidateSyntax("https://evil.example/up"); err == nil {
		t.Error("Expected a host outside the allow-list to be rejected")
	}
}

// TestAllowedHostsAreCaseInsensitive guards both halves of the comparison. DNS
// hostnames are case-insensitive, so neither a mixed-case config entry nor a
// mixed-case endpoint should cause a surprising rejection.
func TestAllowedHostsAreCaseInsensitive(t *testing.T) {
	t.Run("mixed-case endpoint matches a lowercase entry", func(t *testing.T) {
		policy := NewEndpointPolicy()
		policy.SetAllowedHosts([]string{"ntfy.sh"})
		for _, endpoint := range []string{
			"https://NTFY.SH/up",
			"https://Ntfy.Sh/up",
			"HTTPS://NTFY.SH/up",
		} {
			if err := policy.ValidateSyntax(endpoint); err != nil {
				t.Errorf("Expected %s to match the allow-list, got: %v", endpoint, err)
			}
		}
	})

	t.Run("mixed-case config entry matches a lowercase endpoint", func(t *testing.T) {
		policy := NewEndpointPolicy()
		policy.SetAllowedHosts([]string{"  NTFY.SH  ", "", "  "})
		if err := policy.ValidateSyntax("https://ntfy.sh/up"); err != nil {
			t.Errorf("Expected a mixed-case allow-list entry to match, got: %v", err)
		}
		if len(policy.AllowedHosts) != 1 {
			t.Errorf("Expected blank entries to be discarded, got %v", policy.AllowedHosts)
		}
	})

	t.Run("an all-blank list clears the allow-list", func(t *testing.T) {
		policy := NewEndpointPolicy()
		policy.SetAllowedHosts([]string{"", "   "})
		if policy.AllowedHosts != nil {
			t.Errorf("Expected no allow-list, got %v", policy.AllowedHosts)
		}
		if err := policy.ValidateSyntax("https://anything.example/up"); err != nil {
			t.Errorf("Expected no allow-list to mean no restriction, got: %v", err)
		}
	})
}

// TestIPv4MappedIPv6IsJudgedAsIPv4 pins down the behaviour the IPv6 branch's
// comment relies on: net.IP.To4 returns non-nil for ::ffff:a.b.c.d, so those
// addresses are classified by the IPv4 rules and never reach the IPv6 branch.
func TestIPv4MappedIPv6IsJudgedAsIPv4(t *testing.T) {
	blocked := []string{
		"::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
		"::ffff:169.254.169.254",
		"::ffff:192.168.1.1",
	}
	for _, address := range blocked {
		ip := net.ParseIP(address)
		if ip == nil {
			t.Fatalf("Could not parse %s", address)
		}
		if ip.To4() == nil {
			t.Errorf("Expected %s to be treated as IPv4", address)
		}
		if isGloballyRoutable(ip) {
			t.Errorf("%s should not be treated as globally routable", address)
		}
	}
	if !isGloballyRoutable(net.ParseIP("::ffff:1.1.1.1")) {
		t.Error("::ffff:1.1.1.1 should be globally routable")
	}
}

// TestIsGloballyRoutable is the core of the SSRF defence. Every address here is
// one an attacker would like uniqush to connect to on their behalf.
func TestIsGloballyRoutable(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback
		"127.1.2.3",       // loopback, whole /8
		"0.0.0.0",         // unspecified
		"10.0.0.1",        // RFC 1918
		"172.16.0.1",      // RFC 1918
		"172.31.255.254",  // RFC 1918 upper bound
		"192.168.1.1",     // RFC 1918
		"169.254.169.254", // link-local: the cloud metadata endpoint
		"100.64.0.1",      // RFC 6598 carrier-grade NAT
		"192.0.0.1",       // RFC 6890 protocol assignments
		"192.0.2.1",       // RFC 5737 documentation
		"198.51.100.1",    // RFC 5737 documentation
		"203.0.113.1",     // RFC 5737 documentation
		"198.18.0.1",      // RFC 2544 benchmarking
		"224.0.0.1",       // multicast
		"240.0.0.1",       // reserved
		"255.255.255.255", // broadcast
		"::1",             // IPv6 loopback
		"::",              // IPv6 unspecified
		"fc00::1",         // RFC 4193 unique local
		"fd12:3456::1",    // RFC 4193 unique local
		"fe80::1",         // IPv6 link-local
		"ff02::1",         // IPv6 multicast
		"2001:db8::1",     // RFC 3849 documentation
	}
	for _, address := range blocked {
		if isGloballyRoutable(net.ParseIP(address)) {
			t.Errorf("%s should not be treated as globally routable", address)
		}
	}

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34",
		"172.15.0.1",  // just below the RFC 1918 block
		"172.32.0.1",  // just above the RFC 1918 block
		"100.63.0.1",  // just below RFC 6598
		"100.128.0.1", // just above RFC 6598
		"198.20.0.1",  // just above RFC 2544
		"2606:4700::1111",
		"2001:db9::1", // adjacent to the documentation range, but valid
	}
	for _, address := range allowed {
		if !isGloballyRoutable(net.ParseIP(address)) {
			t.Errorf("%s should be treated as globally routable", address)
		}
	}
}

func TestValidateForSendWithLiteralIP(t *testing.T) {
	policy := NewEndpointPolicy()

	if err := policy.ValidateForSend("https://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("Expected the cloud metadata address to be rejected")
	}
	if err := policy.ValidateForSend("https://[::1]:8080/up"); err == nil {
		t.Error("Expected IPv6 loopback to be rejected")
	}
	if err := policy.ValidateForSend("https://1.1.1.1/up"); err != nil {
		t.Errorf("Expected a public literal address to be accepted, got: %v", err)
	}
}

func TestValidateForSendResolvesHostnames(t *testing.T) {
	policy := NewEndpointPolicy()
	policy.resolve = func(host string) ([]net.IP, error) {
		switch host {
		case "public.example":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "private.example":
			return []net.IP{net.ParseIP("10.1.2.3")}, nil
		case "mixed.example":
			// A name returning one public and one private address must not be
			// usable to reach the private one.
			return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}, nil
		case "empty.example":
			return nil, nil
		default:
			return nil, fmt.Errorf("no such host")
		}
	}

	if err := policy.ValidateForSend("https://public.example/up"); err != nil {
		t.Errorf("Expected a public hostname to be accepted, got: %v", err)
	}
	if err := policy.ValidateForSend("https://private.example/up"); err == nil {
		t.Error("Expected a hostname resolving to a private address to be rejected")
	}
	if err := policy.ValidateForSend("https://mixed.example/up"); err == nil {
		t.Error("Expected a hostname with any private address to be rejected")
	}
	if err := policy.ValidateForSend("https://empty.example/up"); err == nil {
		t.Error("Expected a hostname resolving to nothing to be rejected")
	}
	if err := policy.ValidateForSend("https://nxdomain.example/up"); err == nil {
		t.Error("Expected an unresolvable hostname to be rejected")
	}
}

// TestAllowPrivateAddresses covers self-hosted push servers, which the
// UnifiedPush docs call out as a case that must remain possible.
func TestAllowPrivateAddresses(t *testing.T) {
	policy := NewEndpointPolicy()
	policy.AllowPrivateAddresses = true

	if err := policy.ValidateForSend("https://10.1.2.3/up"); err != nil {
		t.Errorf("Expected a private address to be allowed when opted in, got: %v", err)
	}
	// The scheme and length checks still apply.
	if err := policy.ValidateForSend("ftp://10.1.2.3/up"); err == nil {
		t.Error("Expected the scheme check to apply even with private addresses allowed")
	}
}
