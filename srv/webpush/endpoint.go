package webpush

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// maxEndpointLength is the ceiling the UnifiedPush server spec puts on a push
// endpoint: "An endpoint's length MUST be less than or equal to 1000 bytes."
const maxEndpointLength = 1000

// EndpointPolicy decides which push endpoints this server is willing to POST to.
//
// This matters more here than for any other uniqush backend. For APNs, FCM and
// ADM the destination host is a constant compiled into uniqush. For Web Push it
// is supplied by whoever called /subscribe, so without a policy uniqush is an
// open HTTP proxy: an attacker can name any host and have the server issue a
// POST to it from inside the network perimeter.
//
// The UnifiedPush spec asks application servers to restrict the scheme to
// http/https, resolve the host and reject private ranges, and never follow
// redirects. See https://unifiedpush.org/developers/intro/#server-security
type EndpointPolicy struct {
	// AllowPrivateAddresses disables the private-range check. Self-hosted push
	// servers on a LAN are a first-class UnifiedPush use case, so this has to be
	// possible, but it is off by default and should be paired with AllowedHosts.
	AllowPrivateAddresses bool

	// AllowedHosts, when non-empty, is an allow-list of hostnames. Any endpoint
	// whose host is not in the list is rejected regardless of the other checks.
	//
	// Keys must be lowercase. DNS hostnames are case-insensitive, so the lookup
	// lowercases the endpoint's host before checking; an entry with any
	// uppercase in it could never match. Use SetAllowedHosts to get this right.
	AllowedHosts map[string]bool

	// resolve is swappable for tests. Defaults to net.DefaultResolver.LookupIP.
	resolve func(ctx context.Context, network, host string) ([]net.IP, error)
}

// NewEndpointPolicy returns a policy with the safe defaults: https/http only,
// no private address ranges, no allow-list.
func NewEndpointPolicy() *EndpointPolicy {
	return &EndpointPolicy{resolve: net.DefaultResolver.LookupIP}
}

// SetAllowedHosts replaces the allow-list, normalising entries to lowercase and
// discarding blanks. An empty or all-blank list clears the allow-list.
func (p *EndpointPolicy) SetAllowedHosts(hosts []string) {
	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if trimmed := strings.ToLower(strings.TrimSpace(host)); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	if len(allowed) == 0 {
		p.AllowedHosts = nil
		return
	}
	p.AllowedHosts = allowed
}

// ValidateSyntax checks everything about an endpoint that can be judged without
// touching the network. It runs at /subscribe time, so a bad endpoint is
// rejected when someone can still read the error.
func (p *EndpointPolicy) ValidateSyntax(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint is empty")
	}
	if len(endpoint) > maxEndpointLength {
		return fmt.Errorf("endpoint is %d bytes, the maximum is %d", len(endpoint), maxEndpointLength)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL: %v", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "http":
	default:
		return fmt.Errorf("endpoint scheme %q is not allowed, expected https or http", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("endpoint has no host")
	}
	// DNS hostnames are case-insensitive, so HTTPS://NTFY.SH/... must match an
	// allow-list entry of "ntfy.sh".
	if len(p.AllowedHosts) > 0 && !p.AllowedHosts[strings.ToLower(parsed.Hostname())] {
		return fmt.Errorf("endpoint host %q is not in the configured allow-list", parsed.Hostname())
	}
	return nil
}

// ValidateForSend re-checks an endpoint immediately before a push, including
// resolving the host.
//
// This deliberately runs per push rather than once at subscribe time. A name
// that resolved to a public address when the subscription was created can
// resolve to 169.254.169.254 later; checking only at registration is defeated
// by DNS rebinding.
func (p *EndpointPolicy) ValidateForSend(endpoint string) error {
	return p.ValidateForSendContext(context.Background(), endpoint)
}

// ValidateForSendContext checks an endpoint like ValidateForSend, with DNS
// resolution bounded by ctx so it can share a push's request_timeout.
func (p *EndpointPolicy) ValidateForSendContext(ctx context.Context, endpoint string) error {
	if err := p.ValidateSyntax(endpoint); err != nil {
		return err
	}
	if p.AllowPrivateAddresses {
		return nil
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL: %v", err)
	}
	host := parsed.Hostname()

	// A literal IP in the URL needs no lookup.
	if ip := net.ParseIP(host); ip != nil {
		if !isGloballyRoutable(ip) {
			return fmt.Errorf("endpoint address %s is not globally routable", ip)
		}
		return nil
	}

	resolve := p.resolve
	if resolve == nil {
		resolve = net.DefaultResolver.LookupIP
	}
	addrs, err := resolve(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("could not resolve endpoint host %q: %v", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("endpoint host %q resolved to no addresses", host)
	}
	// Every address must be acceptable. A name that returns one public and one
	// private address should not be usable to reach the private one.
	for _, ip := range addrs {
		if !isGloballyRoutable(ip) {
			return fmt.Errorf("endpoint host %q resolves to %s, which is not globally routable", host, ip)
		}
	}
	return nil
}

// checkDialAddress is a net.Dialer Control function, so it sees the address
// the transport is about to connect to. ValidateForSend checks the addresses
// its own lookup returned, but the transport looks the host up again to dial,
// and a DNS server can answer the second lookup with a private address.
func (p *EndpointPolicy) checkDialAddress(_, address string, _ syscall.RawConn) error {
	if p.AllowPrivateAddresses {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if !isGloballyRoutable(net.ParseIP(host)) {
		return fmt.Errorf("refusing to connect to %s, which is not globally routable", host)
	}
	return nil
}

// isGloballyRoutable reports whether an address is one uniqush should be willing
// to connect to on behalf of an untrusted subscriber.
//
// Go has no stdlib equivalent of Rust's IpAddr::is_global, so this enumerates
// the ranges. The UnifiedPush spec calls RFC 1918 and RFC 4193 the minimum bar
// and recommends excluding other non-global ranges; this does the latter.
//
// A NAT64 network that uses its own prefix rather than 64:ff9b::/96 makes
// private IPv4 addresses look like ordinary public IPv6 ones, which nothing
// here can tell apart. Such a network needs an egress firewall rule as well.
func isGloballyRoutable(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		// 0.0.0.0/8 "this network" (RFC 1122)
		case ip4[0] == 0:
			return false
		// 100.64.0.0/10 carrier-grade NAT (RFC 6598)
		case ip4[0] == 100 && ip4[1]&0xc0 == 64:
			return false
		// 192.0.0.0/24 IETF protocol assignments (RFC 6890)
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0:
			return false
		// 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 documentation (RFC 5737)
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2:
			return false
		case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100:
			return false
		case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113:
			return false
		// 198.18.0.0/15 benchmarking (RFC 2544)
		case ip4[0] == 198 && ip4[1]&0xfe == 18:
			return false
		// 240.0.0.0/4 reserved, and 255.255.255.255 broadcast
		case ip4[0] >= 240:
			return false
		}
		return true
	}
	// IPv6. Note that IPv4-mapped addresses such as ::ffff:127.0.0.1 never reach
	// here: To4() above returns non-nil for them, so they are judged as IPv4.
	// IsPrivate already covers fc00::/7 (RFC 4193), and IsUnspecified,
	// IsLoopback and the link-local checks ran earlier.
	switch {
	// 2001:db8::/32 documentation (RFC 3849)
	case ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8:
		return false
	// 3fff::/20 documentation (RFC 9637)
	case ip[0] == 0x3f && ip[1] == 0xff && ip[2]&0xf0 == 0:
		return false
	// 100::/64 discard-only (RFC 6666)
	case bytes.Equal(ip[:8], []byte{0x01, 0, 0, 0, 0, 0, 0, 0}):
		return false
	// 2001:2::/48 benchmarking (RFC 5180)
	case bytes.Equal(ip[:6], []byte{0x20, 0x01, 0x00, 0x02, 0x00, 0x00}):
		return false
	// 5f00::/16 SRv6 segment identifiers (RFC 9602)
	case ip[0] == 0x5f && ip[1] == 0x00:
		return false
	// fec0::/10 deprecated site-local (RFC 3879), still routed privately
	case ip[0] == 0xfe && ip[1]&0xc0 == 0xc0:
		return false
	// 64:ff9b:1::/48 local-use NAT64 (RFC 8215). Where the IPv4 address sits
	// in it depends on the prefix length a network chose, so refuse it all.
	case bytes.Equal(ip[:6], []byte{0x00, 0x64, 0xff, 0x9b, 0x00, 0x01}):
		return false
	// The forms below carry an IPv4 address, and a host with the matching
	// gateway or tunnel reaches that address, so judge it instead.
	// 64:ff9b::/96 NAT64 (RFC 6052)
	case bytes.Equal(ip[:12], []byte{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}):
		return isGloballyRoutable(ip[12:16])
	// 2002::/16 6to4 (RFC 3056)
	case ip[0] == 0x20 && ip[1] == 0x02:
		return isGloballyRoutable(ip[2:6])
	// ::/96 IPv4-compatible (RFC 4291, deprecated)
	case bytes.Equal(ip[:12], make([]byte, 12)):
		return isGloballyRoutable(ip[12:16])
	}
	return true
}
