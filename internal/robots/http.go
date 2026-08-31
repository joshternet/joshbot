package robots

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
)

const maxRedirects = 5

var (
	errInvalidContext = errors.New(
		"robots: invalid context",
	)
	errInvalidInitialOrigin = errors.New(
		"robots: invalid initial origin",
	)
	errHTTPGetterUnavailable = errors.New(
		"robots: HTTP getter unavailable",
	)
	errInvalidHTTPTarget = errors.New(
		"robots: invalid HTTP target",
	)
	errInvalidHTTPResponse = errors.New(
		"robots: invalid HTTP response",
	)
	errRobotsUnreachable = errors.New(
		"robots: resource unreachable",
	)
	errRedirectLimit = errors.New(
		"robots: redirect limit exceeded",
	)
	errInvalidRedirect = errors.New(
		"robots: invalid redirect",
	)
	errUnexpectedStatus = errors.New(
		"robots: unexpected HTTP status",
	)
)

type hopGetter interface {
	get(
		context.Context,
		*url.URL,
	) (*http.Response, error)
}

type guardedHTTP struct {
	resolver netguard.Resolver
	dialer   netguard.Dialer
	rootCAs  *x509.CertPool
}

func (g *guardedHTTP) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	if g == nil {
		return nil, errHTTPGetterUnavailable
	}

	if ctx == nil {
		return nil, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if target == nil {
		return nil, errInvalidHTTPTarget
	}

	targetOrigin, err := origin.Parse(target.String())
	if err != nil {
		return nil, errInvalidHTTPTarget
	}

	destination, err := netguard.Resolve(
		ctx,
		targetOrigin,
		g.resolver,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"robots: resolve target: %w",
			err,
		)
	}

	transport := newGuardedTransport(
		destination,
		g.dialer,
		g.rootCAs,
	)

	logicalTarget := *target
	request := &http.Request{
		Method: http.MethodGet,
		URL:    &logicalTarget,
		Header: make(http.Header),
	}
	request = request.WithContext(ctx)
	request.Header.Set("User-Agent", UserAgent)

	response, err := transport.RoundTrip(request)
	if err != nil {
		transport.CloseIdleConnections()

		if contextError := ctx.Err(); contextError != nil {
			return nil, contextError
		}

		return nil, fmt.Errorf(
			"robots: HTTP request: %w",
			err,
		)
	}

	response.Body = &transportBody{
		ReadCloser: response.Body,
		closeIdle:  transport.CloseIdleConnections,
	}

	return response, nil
}

func newGuardedTransport(
	destination netguard.Destination,
	dialer netguard.Dialer,
	rootCAs *x509.CertPool,
) *http.Transport {
	return &http.Transport{
		Proxy: nil,
		DialContext: func(
			ctx context.Context,
			_ string,
			_ string,
		) (net.Conn, error) {
			return destination.DialContext(ctx, dialer)
		},
		DialTLSContext: nil,
		TLSClientConfig: &tls.Config{
			RootCAs: rootCAs,
		},
	}
}

type transportBody struct {
	io.ReadCloser
	closeIdle func()
}

func (b *transportBody) Close() error {
	err := b.ReadCloser.Close()
	b.closeIdle()

	return err
}

func obtainPolicy(
	ctx context.Context,
	initial origin.Origin,
	getter hopGetter,
) (Policy, error) {
	if ctx == nil {
		return disallowAllPolicy(), errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return disallowAllPolicy(), err
	}

	if initial.String() == "" {
		return disallowAllPolicy(), errInvalidInitialOrigin
	}

	if getter == nil {
		return disallowAllPolicy(), errHTTPGetterUnavailable
	}

	current, _ := url.Parse(
		initial.String() + "/robots.txt",
	)
	redirects := 0

	for {
		response, err := getter.get(ctx, current)
		if err != nil {
			if contextError := ctx.Err(); contextError != nil {
				return disallowAllPolicy(), contextError
			}

			return disallowAllPolicy(), fmt.Errorf(
				"robots: fetch policy: %w",
				err,
			)
		}

		if response == nil || response.Body == nil {
			return disallowAllPolicy(), errInvalidHTTPResponse
		}

		status := response.StatusCode

		if isRedirectStatus(status) {
			_ = response.Body.Close()

			if redirects >= maxRedirects {
				return disallowAllPolicy(), errRedirectLimit
			}

			next, redirectError := redirectTarget(
				current,
				response,
			)
			if redirectError != nil {
				return disallowAllPolicy(), redirectError
			}

			current = next
			redirects++

			continue
		}

		switch {
		case status >= 200 && status <= 299:
			policy, readError := readPolicy(response.Body)
			_ = response.Body.Close()

			if readError != nil {
				return policy, readError
			}

			return policy, nil
		case status >= 400 && status <= 499:
			_ = response.Body.Close()

			return Policy{}, nil
		case status >= 500 && status <= 599:
			_ = response.Body.Close()

			return disallowAllPolicy(), errRobotsUnreachable
		default:
			_ = response.Body.Close()

			return disallowAllPolicy(), errUnexpectedStatus
		}
	}
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func redirectTarget(
	current *url.URL,
	response *http.Response,
) (*url.URL, error) {
	location := response.Header.Get("Location")
	if location == "" {
		return nil, errInvalidRedirect
	}

	next, err := current.Parse(location)
	if err != nil {
		return nil, errInvalidRedirect
	}

	next.Fragment = ""
	next.RawFragment = ""

	if _, err := origin.Parse(next.String()); err != nil {
		return nil, errInvalidRedirect
	}

	return next, nil
}
