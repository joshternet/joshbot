// Package netguard validates network destinations before connection attempts.
package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"

	"github.com/joshternet/joshbot/internal/origin"
)

var (
	errUnsafeAddress       = errors.New("netguard: unsafe address")
	errInvalidContext      = errors.New("netguard: invalid context")
	errInvalidOrigin       = errors.New("netguard: invalid origin")
	errResolverUnavailable = errors.New("netguard: resolver unavailable")
	errResolutionFailed    = errors.New("netguard: resolution failed")
	errNoAddresses         = errors.New(
		"netguard: resolution returned no addresses",
	)
	errDialerUnavailable  = errors.New("netguard: dialer unavailable")
	errInvalidDestination = errors.New("netguard: invalid destination")
	errDialFailed         = errors.New("netguard: dial failed")
)

// blockedIPv4Prefixes follows the IANA IPv4 Special-Purpose Address Space
// registry and the protocol-reserved multicast and future-use ranges.
//
// The policy was reviewed against the registry updated 2025-10-09. JoshBot
// rejects every listed special-purpose block, including protocol-specific
// entries marked globally reachable, because it is a general public-web
// crawler rather than a client for those protocols.
var blockedIPv4Prefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// IANA currently allocates ordinary IPv6 global unicast addresses from
// 2000::/3. Addresses outside this block fail closed.
var publicIPv6Prefix = netip.MustParsePrefix("2000::/3")

// blockedIPv6Prefixes contains special-purpose or returned allocations that
// sit inside 2000::/3. Special ranges outside 2000::/3 are already rejected by
// publicIPv6Prefix.
//
// This policy was reviewed against the IANA IPv6 Special-Purpose Address Space
// and IPv6 Address Space registries updated in October 2025.
var blockedIPv6Prefixes = []netip.Prefix{
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3ffe::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

// ValidateAddr rejects addresses that are unsafe network destinations.
//
// IPv4-mapped IPv6 addresses are normalized before classification so an
// unsafe IPv4 address cannot bypass the IPv4 policy by using mapped syntax.
func ValidateAddr(addr netip.Addr) error {
	addr = addr.Unmap()

	if !addr.IsValid() || addr.Zone() != "" {
		return errUnsafeAddress
	}

	if addr.Is4() {
		for _, prefix := range blockedIPv4Prefixes {
			if prefix.Contains(addr) {
				return errUnsafeAddress
			}
		}

		return nil
	}

	if !publicIPv6Prefix.Contains(addr) {
		return errUnsafeAddress
	}

	for _, prefix := range blockedIPv6Prefixes {
		if prefix.Contains(addr) {
			return errUnsafeAddress
		}
	}

	return nil
}

// Resolver is the DNS operation required to resolve a hostname to IP
// addresses. net.Resolver satisfies this interface.
type Resolver interface {
	LookupNetIP(
		ctx context.Context,
		network string,
		host string,
	) ([]netip.Addr, error)
}

// Dialer is the network operation required to establish a connection.
// net.Dialer satisfies this interface.
type Dialer interface {
	DialContext(
		ctx context.Context,
		network string,
		address string,
	) (net.Conn, error)
}

// Destination is an origin whose exact network addresses have already been
// resolved and validated. Its address set is private so callers cannot replace
// a validated address before dialing.
type Destination struct {
	addresses []netip.Addr
	port      uint16
}

// Resolve validates an IP-literal origin directly or resolves and validates
// every address returned for a hostname. One unsafe or invalid address rejects
// the complete destination.
func Resolve(
	ctx context.Context,
	candidate origin.Origin,
	resolver Resolver,
) (Destination, error) {
	if ctx == nil {
		return Destination{}, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return Destination{}, err
	}

	hostname := candidate.Hostname()
	if hostname == "" {
		return Destination{}, errInvalidOrigin
	}

	if address, err := netip.ParseAddr(hostname); err == nil {
		address = address.Unmap()
		if ValidateAddr(address) != nil {
			return Destination{}, errUnsafeAddress
		}

		return Destination{
			addresses: []netip.Addr{address},
			port:      candidate.Port(),
		}, nil
	}

	if resolver == nil {
		return Destination{}, errResolverUnavailable
	}

	addresses, err := resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Destination{}, contextErr
		}

		return Destination{}, errResolutionFailed
	}

	if len(addresses) == 0 {
		return Destination{}, errNoAddresses
	}

	unique := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if ValidateAddr(address) != nil {
			return Destination{}, errUnsafeAddress
		}

		unique[address] = struct{}{}
	}

	validated := make([]netip.Addr, 0, len(unique))
	for address := range unique {
		validated = append(validated, address)
	}

	slices.SortFunc(
		validated,
		func(left, right netip.Addr) int {
			return left.Compare(right)
		},
	)

	return Destination{
		addresses: validated,
		port:      candidate.Port(),
	}, nil
}

// DialContext attempts the destination's validated addresses in deterministic
// order. Every dial target is an IP literal, preventing a second DNS lookup
// between validation and connection establishment.
func (d Destination) DialContext(
	ctx context.Context,
	dialer Dialer,
) (net.Conn, error) {
	if ctx == nil {
		return nil, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if dialer == nil {
		return nil, errDialerUnavailable
	}

	if len(d.addresses) == 0 {
		return nil, errInvalidDestination
	}

	for _, address := range d.addresses {
		if ValidateAddr(address) != nil {
			return nil, errUnsafeAddress
		}

		target := netip.AddrPortFrom(address, d.port).String()
		connection, err := dialer.DialContext(
			ctx,
			"tcp",
			target,
		)
		if err == nil {
			if connection == nil {
				return nil, errDialFailed
			}

			return connection, nil
		}

		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
	}

	return nil, errDialFailed
}
