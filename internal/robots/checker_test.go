package robots

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestCheckerCachesByInitialOriginFor24Hours(t *testing.T) {
	start := time.Date(
		2026,
		time.August,
		30,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	clock := &checkerClock{
		current: start,
	}
	getter := &fakeHopGetter{
		steps: []hopStep{
			{
				response: redirectResponse(
					http.StatusFound,
					"https://policy.example/robots.txt",
				),
			},
			{
				response: disallowingResponse(),
			},
			{
				response: robotsResponse(
					http.StatusNotFound,
					"",
				),
			},
			{
				response: robotsResponse(
					http.StatusNotFound,
					"",
				),
			},
		},
	}
	checker := newChecker(getter, clock.Now)
	target := mustTarget(
		t,
		"https://example.com/private/page",
	)

	requireCheckerDecision(t, checker, target, false)

	if got := len(getter.targets); got != 2 {
		t.Fatalf(
			"HTTP attempts after initial fetch = %d, want 2",
			got,
		)
	}

	clock.Set(start.Add(23*time.Hour + 59*time.Minute))
	requireCheckerDecision(t, checker, target, false)

	if got := len(getter.targets); got != 2 {
		t.Fatalf(
			"HTTP attempts before expiration = %d, want 2",
			got,
		)
	}

	clock.Set(start.Add(24 * time.Hour))
	requireCheckerDecision(t, checker, target, true)

	if got := len(getter.targets); got != 3 {
		t.Fatalf(
			"HTTP attempts at expiration = %d, want 3",
			got,
		)
	}

	clock.Set(start.Add(47*time.Hour + 59*time.Minute))
	requireCheckerDecision(t, checker, target, true)

	if got := len(getter.targets); got != 3 {
		t.Fatalf(
			"HTTP attempts for cached 4xx policy = %d, want 3",
			got,
		)
	}

	redirectAuthorityTarget := mustTarget(
		t,
		"https://policy.example/private/page",
	)
	requireCheckerDecision(
		t,
		checker,
		redirectAuthorityTarget,
		true,
	)

	wantTargets := []string{
		"https://example.com/robots.txt",
		"https://policy.example/robots.txt",
		"https://example.com/robots.txt",
		"https://policy.example/robots.txt",
	}
	if got := getter.targetStrings(); !slices.Equal(got, wantTargets) {
		t.Errorf(
			"HTTP targets = %v, want %v",
			got,
			wantTargets,
		)
	}
}

func TestCheckerDoesNotCacheFailureOrReviveStalePolicy(
	t *testing.T,
) {
	start := time.Date(
		2026,
		time.August,
		30,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	clock := &checkerClock{
		current: start,
	}
	refreshFailure := errors.New("test refresh failure")
	getter := &fakeHopGetter{
		steps: []hopStep{
			{
				response: robotsResponse(
					http.StatusNotFound,
					"",
				),
			},
			{
				err: refreshFailure,
			},
			{
				response: robotsResponse(
					http.StatusNotFound,
					"",
				),
			},
		},
	}
	checker := newChecker(getter, clock.Now)
	target := mustTarget(
		t,
		"https://example.com/public",
	)

	requireCheckerDecision(t, checker, target, true)

	clock.Set(start.Add(24 * time.Hour))

	allowed, err := checker.Allowed(
		context.Background(),
		target,
	)
	if allowed {
		t.Fatal("Checker.Allowed() = true after failed refresh")
	}

	if !errors.Is(err, refreshFailure) {
		t.Fatalf(
			"Checker.Allowed() error = %v, want refresh failure",
			err,
		)
	}

	requireCheckerDecision(t, checker, target, true)

	if got := len(getter.targets); got != 3 {
		t.Errorf(
			"HTTP attempts = %d, want 3",
			got,
		)
	}
}

func TestCheckerCoalescesConcurrentCacheMisses(t *testing.T) {
	clock := &checkerClock{
		current: time.Date(
			2026,
			time.August,
			30,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}
	getter := &fakeHopGetter{
		steps: []hopStep{
			{
				response: robotsResponse(
					http.StatusNotFound,
					"",
				),
			},
		},
	}
	checker := newChecker(getter, clock.Now)
	target := mustTarget(
		t,
		"https://example.com/public",
	)

	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)

	for range callers {
		go func() {
			<-start

			allowed, err := checker.Allowed(
				context.Background(),
				target,
			)
			if err != nil {
				results <- err

				return
			}

			if !allowed {
				results <- errors.New(
					"Checker.Allowed() = false, want true",
				)

				return
			}

			results <- nil
		}()
	}

	close(start)

	for range callers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}

	if got := len(getter.targets); got != 1 {
		t.Errorf(
			"concurrent robots fetches = %d, want 1",
			got,
		)
	}
}

func TestCheckerRejectsInvalidInputs(t *testing.T) {
	clock := &checkerClock{
		current: time.Date(
			2026,
			time.August,
			30,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}
	target := mustTarget(
		t,
		"https://example.com/public",
	)
	checker := newChecker(
		&fakeHopGetter{},
		clock.Now,
	)

	t.Run("nil receiver", func(t *testing.T) {
		var absent *Checker

		allowed, err := absent.Allowed(
			context.Background(),
			target,
		)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, errCheckerUnavailable) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want unavailable",
				err,
			)
		}
	})

	t.Run("nil context", func(t *testing.T) {
		allowed, err := checker.Allowed(nil, target)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, errInvalidContext) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want invalid context",
				err,
			)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		allowed, err := checker.Allowed(ctx, target)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want canceled",
				err,
			)
		}
	})

	t.Run("nil target", func(t *testing.T) {
		allowed, err := checker.Allowed(
			context.Background(),
			nil,
		)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, errInvalidHTTPTarget) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want invalid target",
				err,
			)
		}
	})

	t.Run("invalid target", func(t *testing.T) {
		allowed, err := checker.Allowed(
			context.Background(),
			&url.URL{
				Scheme: "file",
				Path:   "/private",
			},
		)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, errInvalidHTTPTarget) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want invalid target",
				err,
			)
		}
	})

	t.Run("unavailable getter", func(t *testing.T) {
		unavailable := newChecker(nil, clock.Now)

		allowed, err := unavailable.Allowed(
			context.Background(),
			target,
		)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, errHTTPGetterUnavailable) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want unavailable getter",
				err,
			)
		}
	})

	t.Run("unavailable clock", func(t *testing.T) {
		unavailable := newChecker(
			&fakeHopGetter{},
			nil,
		)

		allowed, err := unavailable.Allowed(
			context.Background(),
			target,
		)
		if allowed {
			t.Fatal("Checker.Allowed() = true, want false")
		}

		if !errors.Is(err, errClockUnavailable) {
			t.Fatalf(
				"Checker.Allowed() error = %v, want unavailable clock",
				err,
			)
		}
	})

	t.Run("production constructor", func(t *testing.T) {
		constructed := NewChecker(
			&robotsResolver{},
			&recordingDialer{},
		)
		if constructed == nil {
			t.Fatal("NewChecker() returned nil")
		}

		if constructed.getter == nil {
			t.Fatal("NewChecker() getter is nil")
		}

		if constructed.now == nil {
			t.Fatal("NewChecker() clock is nil")
		}

		if constructed.cache == nil {
			t.Fatal("NewChecker() cache is nil")
		}
	})
}

func TestCheckerGetEnforcesPolicy(t *testing.T) {
	clock := &checkerClock{
		current: time.Date(
			2026,
			time.August,
			30,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}

	t.Run("denied target is never requested", func(t *testing.T) {
		getter := &fakeHopGetter{
			steps: []hopStep{
				{
					response: robotsResponse(
						http.StatusOK,
						"User-agent: Joshternet-Joshbot\n"+
							"Disallow: /.well-known/\n",
					),
				},
			},
		}
		checker := newChecker(getter, clock.Now)
		target := mustTarget(
			t,
			"https://example.com/.well-known/josh",
		)

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if response != nil {
			_ = response.Body.Close()
			t.Fatal("Checker.Get() response is non-nil")
		}

		if !errors.Is(err, ErrDisallowed) {
			t.Fatalf(
				"Checker.Get() error = %v, want ErrDisallowed",
				err,
			)
		}

		wantTargets := []string{
			"https://example.com/robots.txt",
		}
		if got := getter.targetStrings(); !slices.Equal(
			got,
			wantTargets,
		) {
			t.Errorf(
				"HTTP targets = %v, want %v",
				got,
				wantTargets,
			)
		}
	})

	t.Run("allowed target proceeds", func(t *testing.T) {
		getter := &fakeHopGetter{
			steps: []hopStep{
				{
					response: robotsResponse(
						http.StatusOK,
						"User-agent: Joshternet-Joshbot\n"+
							"Allow: /\n",
					),
				},
				{
					response: robotsResponse(
						http.StatusOK,
						"protected response",
					),
				},
			},
		}
		checker := newChecker(getter, clock.Now)
		target := mustTarget(
			t,
			"https://example.com/.well-known/josh",
		)

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if err != nil {
			t.Fatalf(
				"Checker.Get() error = %v, want nil",
				err,
			)
		}

		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("reading protected response: %v", err)
		}

		if err := response.Body.Close(); err != nil {
			t.Fatalf("closing protected response: %v", err)
		}

		if got := string(body); got != "protected response" {
			t.Errorf(
				"protected response body = %q, want %q",
				got,
				"protected response",
			)
		}

		wantTargets := []string{
			"https://example.com/robots.txt",
			"https://example.com/.well-known/josh",
		}
		if got := getter.targetStrings(); !slices.Equal(
			got,
			wantTargets,
		) {
			t.Errorf(
				"HTTP targets = %v, want %v",
				got,
				wantTargets,
			)
		}
	})
}

func TestCheckerGetFailsClosed(t *testing.T) {
	clock := &checkerClock{
		current: time.Date(
			2026,
			time.August,
			30,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}
	target := mustTarget(
		t,
		"https://example.com/private",
	)

	t.Run("policy fetch failure", func(t *testing.T) {
		fetchFailure := errors.New("test policy fetch failure")
		getter := &fakeHopGetter{
			steps: []hopStep{
				{
					err: fetchFailure,
				},
			},
		}
		checker := newChecker(getter, clock.Now)

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if response != nil {
			_ = response.Body.Close()
			t.Fatal("Checker.Get() response is non-nil")
		}

		if !errors.Is(err, fetchFailure) {
			t.Fatalf(
				"Checker.Get() error = %v, want policy failure",
				err,
			)
		}

		if got := len(getter.targets); got != 1 {
			t.Errorf(
				"HTTP attempts = %d, want 1",
				got,
			)
		}
	})

	t.Run("protected fetch failure", func(t *testing.T) {
		fetchFailure := errors.New("test protected fetch failure")
		getter := &fakeHopGetter{
			steps: []hopStep{
				{
					response: robotsResponse(
						http.StatusNotFound,
						"",
					),
				},
				{
					err: fetchFailure,
				},
			},
		}
		checker := newChecker(getter, clock.Now)

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if response != nil {
			_ = response.Body.Close()
			t.Fatal("Checker.Get() response is non-nil")
		}

		if !errors.Is(err, fetchFailure) {
			t.Fatalf(
				"Checker.Get() error = %v, want protected failure",
				err,
			)
		}

		if got := len(getter.targets); got != 2 {
			t.Errorf(
				"HTTP attempts = %d, want 2",
				got,
			)
		}
	})

	t.Run("nil protected response", func(t *testing.T) {
		getter := &fakeHopGetter{
			steps: []hopStep{
				{
					response: robotsResponse(
						http.StatusNotFound,
						"",
					),
				},
				{},
			},
		}
		checker := newChecker(getter, clock.Now)

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if response != nil {
			_ = response.Body.Close()
			t.Fatal("Checker.Get() response is non-nil")
		}

		if !errors.Is(err, errInvalidHTTPResponse) {
			t.Fatalf(
				"Checker.Get() error = %v, want invalid response",
				err,
			)
		}
	})

	t.Run("missing protected response body", func(t *testing.T) {
		getter := &fakeHopGetter{
			steps: []hopStep{
				{
					response: robotsResponse(
						http.StatusNotFound,
						"",
					),
				},
				{
					response: &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
					},
				},
			},
		}
		checker := newChecker(getter, clock.Now)

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if response != nil {
			t.Fatal("Checker.Get() response is non-nil")
		}

		if !errors.Is(err, errInvalidHTTPResponse) {
			t.Fatalf(
				"Checker.Get() error = %v, want invalid response",
				err,
			)
		}
	})
}

func TestCheckerGetUsesJoshBotIdentityForRobotsAndTarget(
	t *testing.T,
) {
	var observationLock sync.Mutex
	observations := make([]checkerRequestObservation, 0, 2)

	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			observationLock.Lock()
			observations = append(
				observations,
				checkerRequestObservation{
					host:      request.Host,
					path:      request.URL.Path,
					userAgent: request.UserAgent(),
				},
			)
			observationLock.Unlock()

			if request.URL.Path == "/robots.txt" {
				_, _ = writer.Write([]byte(
					"User-agent: Joshternet-Joshbot\n" +
						"Allow: /\n",
				))

				return
			}

			_, _ = writer.Write([]byte("target response"))
		}),
	)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	t.Cleanup(server.Close)

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{
		dial: func(
			ctx context.Context,
			network string,
			_ string,
		) (net.Conn, error) {
			var local net.Dialer

			return local.DialContext(
				ctx,
				network,
				server.Listener.Addr().String(),
			)
		},
	}
	clock := &checkerClock{
		current: time.Date(
			2026,
			time.August,
			30,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}
	checker := newChecker(
		&guardedHTTP{
			resolver: resolver,
			dialer:   dialer,
		},
		clock.Now,
	)
	target := mustTarget(
		t,
		"http://example.com/.well-known/josh",
	)

	response, err := checker.Get(
		context.Background(),
		target,
	)
	if err != nil {
		t.Fatalf("Checker.Get() error = %v, want nil", err)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading protected response: %v", err)
	}

	if err := response.Body.Close(); err != nil {
		t.Fatalf("closing protected response: %v", err)
	}

	if got := string(body); got != "target response" {
		t.Errorf(
			"protected response body = %q, want %q",
			got,
			"target response",
		)
	}

	observationLock.Lock()
	gotObservations := append(
		[]checkerRequestObservation(nil),
		observations...,
	)
	observationLock.Unlock()

	wantObservations := []checkerRequestObservation{
		{
			host:      "example.com",
			path:      "/robots.txt",
			userAgent: UserAgent,
		},
		{
			host:      "example.com",
			path:      "/.well-known/josh",
			userAgent: UserAgent,
		},
	}
	if !slices.Equal(gotObservations, wantObservations) {
		t.Errorf(
			"HTTP observations = %v, want %v",
			gotObservations,
			wantObservations,
		)
	}

	wantAddresses := []string{
		"93.184.216.34:80",
		"93.184.216.34:80",
	}
	if got := dialer.addressSnapshot(); !slices.Equal(
		got,
		wantAddresses,
	) {
		t.Errorf(
			"dial addresses = %v, want %v",
			got,
			wantAddresses,
		)
	}
}

type checkerClock struct {
	mu      sync.Mutex
	current time.Time
}

func (c *checkerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.current
}

func (c *checkerClock) Set(current time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.current = current
}

type checkerRequestObservation struct {
	host      string
	path      string
	userAgent string
}

func requireCheckerDecision(
	t *testing.T,
	checker *Checker,
	target *url.URL,
	want bool,
) {
	t.Helper()

	got, err := checker.Allowed(
		context.Background(),
		target,
	)
	if err != nil {
		t.Fatalf(
			"Checker.Allowed() error = %v, want nil",
			err,
		)
	}

	if got != want {
		t.Fatalf(
			"Checker.Allowed() = %t, want %t",
			got,
			want,
		)
	}
}
