package robots_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

var (
	errRobotsIntegrationResolve = errors.New(
		"integration resolver failure",
	)
	errRobotsIntegrationSign = errors.New(
		"integration signer failure",
	)
	errRobotsIntegrationDial = errors.New(
		"integration dial failure",
	)
)

type robotsFailureIntegrationResolver struct {
	err error
}

func (resolver robotsFailureIntegrationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return nil, resolver.err
}

type robotsFailureIntegrationDialer struct {
	cancel context.CancelFunc
	err    error
}

func (dialer robotsFailureIntegrationDialer) DialContext(
	context.Context,
	string,
	string,
) (net.Conn, error) {
	if dialer.cancel != nil {
		dialer.cancel()
	}

	return nil, dialer.err
}

type robotsFailureIntegrationSigner struct {
	err error
}

func (signer robotsFailureIntegrationSigner) Sign(
	*http.Request,
) error {
	return signer.err
}

func TestRobotsHTTPFailureIntegrationPropagatesBoundaryFailures(
	t *testing.T,
) {
	target := mustRobotsIntegrationURL(
		t,
		"http://example.com/page",
	)

	t.Run("resolver failure", func(t *testing.T) {
		checker := robots.NewCheckerWithRequestDelayAndSigner(
			robotsFailureIntegrationResolver{
				err: errRobotsIntegrationResolve,
			},
			robotsFailureIntegrationDialer{
				err: errRobotsIntegrationDial,
			},
			0,
			nil,
		)

		allowed, err := checker.Allowed(
			context.Background(),
			target,
		)

		if allowed {
			t.Error(
				"Checker.Allowed() = true, want false",
			)
		}

		if !errors.Is(
			err,
			robots.ErrTemporary,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want %v",
				err,
				robots.ErrTemporary,
			)
		}

		if got := netguard.FailureCategory(err); got != retry.CategoryDNS {
			t.Errorf(
				"Checker.Allowed() failure category = %v, want %v",
				got,
				retry.CategoryDNS,
			)
		}
	})

	t.Run("signer failure", func(t *testing.T) {
		checker := robots.NewCheckerWithRequestDelayAndSigner(
			robotsIntegrationResolver{},
			robotsFailureIntegrationDialer{
				err: errRobotsIntegrationDial,
			},
			0,
			robotsFailureIntegrationSigner{
				err: errRobotsIntegrationSign,
			},
		)

		allowed, err := checker.Allowed(
			context.Background(),
			target,
		)

		if allowed {
			t.Error(
				"Checker.Allowed() = true, want false",
			)
		}

		if !errors.Is(
			err,
			robots.ErrTemporary,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want %v",
				err,
				robots.ErrTemporary,
			)
		}

		if !errors.Is(
			err,
			errRobotsIntegrationSign,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want signer failure",
				err,
			)
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		checker := robots.NewCheckerWithRequestDelayAndSigner(
			robotsIntegrationResolver{},
			robotsFailureIntegrationDialer{
				err: errRobotsIntegrationDial,
			},
			0,
			nil,
		)

		allowed, err := checker.Allowed(
			context.Background(),
			target,
		)

		if allowed {
			t.Error(
				"Checker.Allowed() = true, want false",
			)
		}

		if !errors.Is(
			err,
			robots.ErrTemporary,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want %v",
				err,
				robots.ErrTemporary,
			)
		}

		if got := netguard.FailureCategory(err); got != retry.CategoryTransport {
			t.Errorf(
				"Checker.Allowed() failure category = %v, want %v",
				got,
				retry.CategoryTransport,
			)
		}
	})

	t.Run("transport cancellation wins dial failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		checker := robots.NewCheckerWithRequestDelayAndSigner(
			robotsIntegrationResolver{},
			robotsFailureIntegrationDialer{
				cancel: cancel,
				err:    errRobotsIntegrationDial,
			},
			0,
			nil,
		)

		allowed, err := checker.Allowed(
			ctx,
			target,
		)

		if allowed {
			t.Error(
				"Checker.Allowed() = true, want false",
			)
		}

		if !errors.Is(
			err,
			robots.ErrTemporary,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want %v",
				err,
				robots.ErrTemporary,
			)
		}

		if !errors.Is(
			err,
			context.Canceled,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want context.Canceled",
				err,
			)
		}

		if errors.Is(
			err,
			errRobotsIntegrationDial,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want cancellation to win dial failure",
				err,
			)
		}
	})

	t.Run("scheduler failure stops request", func(t *testing.T) {
		checker := robots.NewCheckerWithRequestDelayAndSigner(
			robotsIntegrationResolver{},
			robotsFailureIntegrationDialer{
				err: errRobotsIntegrationDial,
			},
			-time.Second,
			nil,
		)

		allowed, err := checker.Allowed(
			context.Background(),
			target,
		)

		if allowed {
			t.Error(
				"Checker.Allowed() = true, want false",
			)
		}

		if !errors.Is(
			err,
			robots.ErrTemporary,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, want %v",
				err,
				robots.ErrTemporary,
			)
		}

		if errors.Is(
			err,
			errRobotsIntegrationDial,
		) {
			t.Errorf(
				"Checker.Allowed() error = %v, request should fail before dialing",
				err,
			)
		}
	})
}
