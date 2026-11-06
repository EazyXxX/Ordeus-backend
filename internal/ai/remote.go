package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

var (
	errInvalidRemoteURL = errors.New("api address must be a public https url")
	errBlockedAddress   = errors.New("address is not allowed")
)

// sharedAddressSpace is the carrier-grade NAT range, which is not public.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() &&
		address.IsGlobalUnicast() &&
		!address.IsPrivate() &&
		!address.IsLoopback() &&
		!address.IsLinkLocalUnicast() &&
		!sharedAddressSpace.Contains(address)
}

// normalizeRemoteURL validates an address typed by a user. The server sends
// requests there, so it must not become a way to reach internal services.
func normalizeRemoteURL(raw string, allowPrivate bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errInvalidRemoteURL
	}
	if parsed.Scheme != "https" && !(allowPrivate && parsed.Scheme == "http") {
		return "", errInvalidRemoteURL
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

// newRemoteHTTPClient checks the address at connection time, after DNS, so a
// public name that resolves to an internal address is refused too. Redirects
// are refused because they would carry the request to an unchecked place.
func newRemoteHTTPClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			parsed, err := netip.ParseAddrPort(address)
			if err != nil || !isPublicAddress(parsed.Addr()) {
				return fmt.Errorf("%w: %s", errBlockedAddress, address)
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
