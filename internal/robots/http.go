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
	"sync"
	"time"

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

// RequestSigner applies authentication metadata to one outbound JoshBot HTTP
// request.
//
// Sign is called after JoshBot's stable User-Agent has been applied and
// immediately before the guarded transport sends the request. Redirect hops
// therefore receive a fresh signature for the authority actually contacted.
type RequestSigner interface {
	Sign(*http.Request) error
}

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
	signer   RequestSigner
	config   HTTPConfig
	budget   FleetBudget

	pendingMu    sync.Mutex
	pendingBytes int64
}

func (g *guardedHTTP) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	if err := g.reserve(ctx); err != nil {
		return nil, err
	}

	// Checker validates initial request targets before they reach this
	// boundary, and redirectTarget applies the same validation to every
	// redirect hop.
	targetOrigin, _ := origin.Parse(target.String())

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
		g.config,
	)

	logicalTarget := *target
	request := &http.Request{
		Method: http.MethodGet,
		URL:    &logicalTarget,
		Header: make(http.Header),
	}
	request = request.WithContext(ctx)
	request.Header.Set("User-Agent", UserAgent)

	if g.signer != nil {
		if err := g.signer.Sign(request); err != nil {
			transport.CloseIdleConnections()

			return nil, fmt.Errorf(
				"robots: sign HTTP request: %w",
				err,
			)
		}
	}

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

	body := &transportBody{
		ReadCloser: response.Body,
		closeIdle:  transport.CloseIdleConnections,
	}
	if g.budget == nil {
		response.Body = body
	} else {
		response.Body = &fleetBudgetBody{
			ReadCloser: body,
			add:        g.addPending,
		}
	}

	return response, nil
}

// reserve adds pending body bytes and waits for the shared request slot.
// A nil budget means this client is not part of the crawl fleet.
func (g *guardedHTTP) reserve(ctx context.Context) error {
	if g == nil || g.budget == nil {
		return nil
	}

	g.pendingMu.Lock()
	pending := g.pendingBytes
	g.pendingMu.Unlock()

	wait, err := g.budget.Acquire(ctx, pending)
	if err != nil {
		var budget fleetByteBudgetDelay
		if errors.As(err, &budget) {
			g.clearPending(pending)

			return &FleetByteBudgetError{
				RetryAfter: budget.FleetByteBudgetDelay(),
			}
		}

		return fmt.Errorf(
			"robots: fleet budget: %w",
			err,
		)
	}

	g.clearPending(pending)
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *guardedHTTP) addPending(count int64) {
	if g == nil || count <= 0 {
		return
	}

	g.pendingMu.Lock()
	g.pendingBytes += count
	g.pendingMu.Unlock()
}

func (g *guardedHTTP) clearPending(sent int64) {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()

	if g.pendingBytes >= sent {
		g.pendingBytes -= sent

		return
	}

	g.pendingBytes = 0
}

type fleetBudgetBody struct {
	io.ReadCloser
	add func(int64)
}

func (body *fleetBudgetBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if count > 0 && body.add != nil {
		body.add(int64(count))
	}

	return count, err
}

func newGuardedTransport(
	destination netguard.Destination,
	dialer netguard.Dialer,
	rootCAs *x509.CertPool,
	config HTTPConfig,
) *http.Transport {
	if config == (HTTPConfig{}) {
		config = DefaultHTTPConfig()
	}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(
			ctx context.Context,
			_ string,
			_ string,
		) (net.Conn, error) {
			return destination.DialContextWithTimeout(ctx, dialer, config.DialTimeout)
		},
		DialTLSContext:        nil,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
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

			if errors.Is(err, ErrFleetByteBudget) {
				return disallowAllPolicy(), err
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
