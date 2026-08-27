// Package origin parses and canonicalizes web-origin identities from absolute
// HTTP and HTTPS URLs.
//
// Parsing is a pure local operation. It performs no network requests or DNS
// resolution.
package origin

import (
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// idnaProfile applies the nontransitional Unicode mapping used for web hosts.
var idnaProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.Transitional(false),
	idna.CheckHyphens(false),
	idna.StrictDomainName(false),
	idna.VerifyDNSLength(false),
)

// endsInNumberPattern implements the WHATWG "ends in a number" host check.
// Standard IPv4 literals are handled by netip before this pattern is used.
var endsInNumberPattern = regexp.MustCompile(
	`(^|\.)([0-9]+|0[xX][0-9A-Fa-f]*)\.?$`,
)

// hasForbiddenDomainCodePoint reports whether an ASCII host contains a code
// point forbidden by the WHATWG domain parser.
func hasForbiddenDomainCodePoint(host string) bool {
	return strings.IndexFunc(host, func(r rune) bool {
		return r <= ' ' || r == '\x7f' ||
			strings.ContainsRune("#%/:<>?@[\\]^|", r)
	}) >= 0
}

// Origin is the canonical web-origin identity returned by Parse.
//
// Origin values are comparable and may be used as map keys. The zero value does
// not represent a valid origin.
type Origin struct {
	serialization string
}

// Parse derives a canonical web origin from an absolute HTTP or HTTPS URL.
//
// Parse rejects URLs that cannot establish a supported web origin, contain
// user information, or use a nonstandard numeric IPv4 form. Path, query, and
// fragment components do not affect the returned origin. Parse performs no
// network requests or DNS resolution.
func Parse(rawURL string) (Origin, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Origin{}, errors.New("origin: invalid URL")
	}

	switch parsed.Scheme {
	case "http", "https":
	default:
		return Origin{}, errors.New("origin: unsupported scheme")
	}

	if parsed.User != nil {
		return Origin{}, errors.New("origin: credentials are not allowed")
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return Origin{}, errors.New("origin: missing host")
	}

	host := hostname
	address, addressErr := netip.ParseAddr(hostname)
	if addressErr == nil {
		if address.Zone() != "" {
			return Origin{}, errors.New("origin: IPv6 zone identifiers are not allowed")
		}

		if address.Is6() {
			host = "[" + address.String() + "]"
		}
	} else {
		asciiHost, idnaErr := idnaProfile.ToASCII(hostname)
		if idnaErr != nil ||
			asciiHost == "" ||
			hasForbiddenDomainCodePoint(asciiHost) {
			return Origin{}, errors.New("origin: invalid host")
		}

		if endsInNumberPattern.MatchString(asciiHost) {
			return Origin{}, errors.New("origin: nonstandard numeric IPv4 form")
		}

		host = asciiHost
	}

	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return Origin{}, errors.New("origin: invalid port")
		}

		defaultPort := (parsed.Scheme == "http" && portNumber == 80) ||
			(parsed.Scheme == "https" && portNumber == 443)
		if !defaultPort {
			host += ":" + strconv.FormatUint(portNumber, 10)
		}
	}

	return Origin{
		serialization: parsed.Scheme + "://" + host,
	}, nil
}

// String returns the origin's canonical ASCII serialization without a trailing
// slash. It returns an empty string for the zero value.
func (o Origin) String() string {
	return o.serialization
}
