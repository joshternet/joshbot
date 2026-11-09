package origin

import (
	"net/netip"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// NeighborhoodKey returns the shared pacing and claim group for a canonical
// ASCII hostname.
//
// It reports false for an IP address and for a host that is only an ICANN
// public suffix. Those hosts keep an origin-only clock and claim key. A private
// multi-label public suffix, including its apex, is the group. Every other host
// uses its eTLD+1.
//
// @param hostname - Canonical ASCII hostname without IPv6 brackets.
// @returns The neighborhood key and whether a shared group applies.
func NeighborhoodKey(hostname string) (string, bool) {
	if _, err := netip.ParseAddr(hostname); err == nil {
		return "", false
	}

	suffix, icann := publicsuffix.PublicSuffix(hostname)
	if !icann && strings.Contains(suffix, ".") {
		return suffix, true
	}

	registrable, err := publicsuffix.EffectiveTLDPlusOne(hostname)
	if err != nil {
		return "", false
	}
	return registrable, true
}
