//lint:file-ignore SA1012 Intentional negative tests verify defensive nil-context rejection; production callers must never pass a nil context.
package robots

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

var (
	errRobotsBoundaryIntegrationFetch = errors.New(
		"integration robots fetch failure",
	)
	errRobotsBoundaryIntegrationRead = errors.New(
		"integration robots read failure",
	)
	errRobotsBoundaryIntegrationWait = errors.New(
		"integration robots wait failure",
	)
)

type robotsBoundaryIntegrationGetter struct {
	steps   []robotsBoundaryIntegrationStep
	targets []string
}

type robotsBoundaryIntegrationStep struct {
	response *http.Response
	err      error
}

func (getter *robotsBoundaryIntegrationGetter) get(
	_ context.Context,
	target *url.URL,
) (*http.Response, error) {
	getter.targets = append(
		getter.targets,
		target.String(),
	)

	if len(getter.steps) == 0 {
		return nil, errRobotsBoundaryIntegrationFetch
	}

	step := getter.steps[0]
	getter.steps = getter.steps[1:]

	return step.response, step.err
}

type robotsBoundaryIntegrationBody struct {
	reader  io.Reader
	readErr error
	closed  bool
}

func (body *robotsBoundaryIntegrationBody) Read(
	buffer []byte,
) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}

	return body.reader.Read(buffer)
}

func (body *robotsBoundaryIntegrationBody) Close() error {
	body.closed = true
	return nil
}

type robotsBoundaryIntegrationClock struct {
	current time.Time
}

func (clock *robotsBoundaryIntegrationClock) Now() time.Time {
	return clock.current
}

type robotsBoundaryIntegrationWaiter struct {
	clock     *robotsBoundaryIntegrationClock
	durations []time.Duration
	err       error
}

func (waiter *robotsBoundaryIntegrationWaiter) Wait(
	_ context.Context,
	delay time.Duration,
) error {
	waiter.durations = append(
		waiter.durations,
		delay,
	)

	if waiter.err != nil {
		return waiter.err
	}

	if waiter.clock != nil {
		waiter.clock.current = waiter.clock.current.Add(delay)
	}

	return nil
}

func TestRobotsBoundaryIntegrationCheckerValidationAndCache(
	t *testing.T,
) {
	start := time.Date(
		2026,
		time.September,
		19,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	clock := &robotsBoundaryIntegrationClock{
		current: start,
	}
	target := robotsBoundaryIntegrationTarget(
		t,
		"https://example.com/private",
	)

	var missing *Checker
	allowed, err := missing.Allowed(
		context.Background(),
		target,
	)
	if allowed || !errors.Is(err, errCheckerUnavailable) {
		t.Errorf(
			"nil Checker.Allowed() = %t, %v, want false, %v",
			allowed,
			err,
			errCheckerUnavailable,
		)
	}

	checker := newChecker(
		&robotsBoundaryIntegrationGetter{},
		clock.Now,
	)
	if checker == nil || checker.scheduler == nil || checker.cache == nil {
		t.Fatalf(
			"newChecker() = %#v, want initialized checker",
			checker,
		)
	}

	if allowed, err := checker.Allowed(
		nil,
		target,
	); allowed || !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Allowed(nil) = %t, %v, want false, %v",
			allowed,
			err,
			errInvalidContext,
		)
	}

	canceled, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if allowed, err := checker.Allowed(
		canceled,
		target,
	); allowed || !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Allowed(canceled) = %t, %v, want false, context.Canceled",
			allowed,
			err,
		)
	}

	if allowed, err := checker.Allowed(
		context.Background(),
		nil,
	); allowed || !errors.Is(err, errInvalidHTTPTarget) {
		t.Errorf(
			"Allowed(nil target) = %t, %v, want false, %v",
			allowed,
			err,
			errInvalidHTTPTarget,
		)
	}

	if allowed, err := checker.Allowed(
		context.Background(),
		&url.URL{
			Scheme: "file",
			Path:   "/private",
		},
	); allowed || !errors.Is(err, errInvalidHTTPTarget) {
		t.Errorf(
			"Allowed(invalid target) = %t, %v, want false, %v",
			allowed,
			err,
			errInvalidHTTPTarget,
		)
	}

	if allowed, err := newChecker(
		nil,
		clock.Now,
	).Allowed(
		context.Background(),
		target,
	); allowed || !errors.Is(err, errHTTPGetterUnavailable) {
		t.Errorf(
			"Allowed(nil getter) = %t, %v, want false, %v",
			allowed,
			err,
			errHTTPGetterUnavailable,
		)
	}

	if allowed, err := newChecker(
		&robotsBoundaryIntegrationGetter{},
		nil,
	).Allowed(
		context.Background(),
		target,
	); allowed || !errors.Is(err, errClockUnavailable) {
		t.Errorf(
			"Allowed(nil clock) = %t, %v, want false, %v",
			allowed,
			err,
			errClockUnavailable,
		)
	}

	getter := &robotsBoundaryIntegrationGetter{
		steps: []robotsBoundaryIntegrationStep{
			{
				response: robotsBoundaryIntegrationResponse(
					http.StatusNotFound,
					"",
				),
			},
			{
				response: robotsBoundaryIntegrationResponse(
					http.StatusOK,
					"User-agent: Joshternet-Joshbot\n"+
						"Disallow: /private\n",
				),
			},
		},
	}
	cached := newChecker(
		getter,
		clock.Now,
	)

	allowed, err = cached.Allowed(
		context.Background(),
		target,
	)
	if err != nil || !allowed {
		t.Fatalf(
			"first Allowed() = %t, %v, want true, nil",
			allowed,
			err,
		)
	}

	allowed, err = cached.Allowed(
		context.Background(),
		target,
	)
	if err != nil || !allowed {
		t.Fatalf(
			"cached Allowed() = %t, %v, want true, nil",
			allowed,
			err,
		)
	}

	if len(getter.targets) != 1 {
		t.Fatalf(
			"robots requests before expiry = %d, want 1",
			len(getter.targets),
		)
	}

	clock.current = start.Add(maxCacheLifetime)

	allowed, err = cached.Allowed(
		context.Background(),
		target,
	)
	if err != nil || allowed {
		t.Errorf(
			"refreshed Allowed() = %t, %v, want false, nil",
			allowed,
			err,
		)
	}

	if len(getter.targets) != 2 {
		t.Errorf(
			"robots requests after expiry = %d, want 2",
			len(getter.targets),
		)
	}

	malformedDelay := newChecker(
		&robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					response: robotsBoundaryIntegrationResponse(
						http.StatusOK,
						"User-agent: Joshternet-Joshbot\n"+
							"Crawl-delay: garbage\n"+
							"Allow: /\n",
					),
				},
			},
		},
		clock.Now,
	)

	allowed, err = malformedDelay.Allowed(
		context.Background(),
		target,
	)
	if allowed ||
		!errors.Is(err, ErrTemporary) ||
		!errors.Is(err, ErrInvalidCrawlDelay) {
		t.Errorf(
			"Allowed(malformed Crawl-delay) = %t, %v, want false wrapped crawl-delay error",
			allowed,
			err,
		)
	}
}

func TestRobotsBoundaryIntegrationCheckerGetFailures(
	t *testing.T,
) {
	clock := &robotsBoundaryIntegrationClock{
		current: time.Date(
			2026,
			time.September,
			19,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}
	target := robotsBoundaryIntegrationTarget(
		t,
		"https://example.com/private",
	)

	t.Run("policy failure", func(t *testing.T) {
		getter := &robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					err: errRobotsBoundaryIntegrationFetch,
				},
			},
		}

		response, err := newChecker(
			getter,
			clock.Now,
		).Get(
			context.Background(),
			target,
		)
		if response != nil ||
			!errors.Is(err, ErrTemporary) ||
			!errors.Is(
				err,
				errRobotsBoundaryIntegrationFetch,
			) {
			t.Errorf(
				"Get() = %#v, %v, want nil wrapped temporary fetch failure",
				response,
				err,
			)
		}
	})

	t.Run("denied", func(t *testing.T) {
		getter := &robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					response: robotsBoundaryIntegrationResponse(
						http.StatusOK,
						"User-agent: Joshternet-Joshbot\n"+
							"Disallow: /private\n",
					),
				},
			},
		}

		response, err := newChecker(
			getter,
			clock.Now,
		).Get(
			context.Background(),
			target,
		)
		if response != nil ||
			!errors.Is(err, ErrDisallowed) {
			t.Errorf(
				"Get() = %#v, %v, want nil, %v",
				response,
				err,
				ErrDisallowed,
			)
		}

		if len(getter.targets) != 1 {
			t.Errorf(
				"denied getter calls = %d, want 1",
				len(getter.targets),
			)
		}
	})

	t.Run("protected fetch failure", func(t *testing.T) {
		getter := &robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					response: robotsBoundaryIntegrationResponse(
						http.StatusNotFound,
						"",
					),
				},
				{
					err: errRobotsBoundaryIntegrationFetch,
				},
			},
		}

		response, err := newChecker(
			getter,
			clock.Now,
		).Get(
			context.Background(),
			target,
		)
		if response != nil ||
			!errors.Is(
				err,
				errRobotsBoundaryIntegrationFetch,
			) {
			t.Errorf(
				"Get() = %#v, %v, want nil wrapped protected fetch failure",
				response,
				err,
			)
		}
	})

	for _, test := range []struct {
		name string
		step robotsBoundaryIntegrationStep
	}{
		{
			name: "nil protected response",
			step: robotsBoundaryIntegrationStep{},
		},
		{
			name: "nil protected body",
			step: robotsBoundaryIntegrationStep{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			getter := &robotsBoundaryIntegrationGetter{
				steps: []robotsBoundaryIntegrationStep{
					{
						response: robotsBoundaryIntegrationResponse(
							http.StatusNotFound,
							"",
						),
					},
					test.step,
				},
			}

			response, err := newChecker(
				getter,
				clock.Now,
			).Get(
				context.Background(),
				target,
			)
			if response != nil ||
				!errors.Is(
					err,
					errInvalidHTTPResponse,
				) {
				t.Errorf(
					"Get() = %#v, %v, want nil, %v",
					response,
					err,
					errInvalidHTTPResponse,
				)
			}
		})
	}
}

func TestRobotsBoundaryIntegrationObtainPolicyFailures(
	t *testing.T,
) {
	initial := robotsBoundaryIntegrationOrigin(
		t,
		"https://example.com",
	)
	private := robotsBoundaryIntegrationTarget(
		t,
		"https://example.com/private",
	)

	policy, err := obtainPolicy(
		nil,
		initial,
		&robotsBoundaryIntegrationGetter{},
	)
	if !errors.Is(err, errInvalidContext) ||
		policy.Allowed(private) {
		t.Errorf(
			"obtainPolicy(nil) error = %v, allowed = true",
			err,
		)
	}

	canceled, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	policy, err = obtainPolicy(
		canceled,
		initial,
		&robotsBoundaryIntegrationGetter{},
	)
	if !errors.Is(err, context.Canceled) ||
		policy.Allowed(private) {
		t.Errorf(
			"obtainPolicy(canceled) error = %v, allowed = true",
			err,
		)
	}

	policy, err = obtainPolicy(
		context.Background(),
		origin.Origin{},
		&robotsBoundaryIntegrationGetter{},
	)
	if !errors.Is(err, errInvalidInitialOrigin) ||
		policy.Allowed(private) {
		t.Errorf(
			"obtainPolicy(zero origin) error = %v, allowed = true",
			err,
		)
	}

	policy, err = obtainPolicy(
		context.Background(),
		initial,
		nil,
	)
	if !errors.Is(err, errHTTPGetterUnavailable) ||
		policy.Allowed(private) {
		t.Errorf(
			"obtainPolicy(nil getter) error = %v, allowed = true",
			err,
		)
	}

	tests := []struct {
		name    string
		step    robotsBoundaryIntegrationStep
		wantErr error
		allowed bool
	}{
		{
			name: "network failure",
			step: robotsBoundaryIntegrationStep{
				err: errRobotsBoundaryIntegrationFetch,
			},
			wantErr: errRobotsBoundaryIntegrationFetch,
		},
		{
			name:    "nil response",
			step:    robotsBoundaryIntegrationStep{},
			wantErr: errInvalidHTTPResponse,
		},
		{
			name: "nil body",
			step: robotsBoundaryIntegrationStep{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
				},
			},
			wantErr: errInvalidHTTPResponse,
		},
		{
			name: "4xx allows",
			step: robotsBoundaryIntegrationStep{
				response: robotsBoundaryIntegrationResponse(
					http.StatusNotFound,
					"",
				),
			},
			allowed: true,
		},
		{
			name: "5xx fails closed",
			step: robotsBoundaryIntegrationStep{
				response: robotsBoundaryIntegrationResponse(
					http.StatusServiceUnavailable,
					"",
				),
			},
			wantErr: errRobotsUnreachable,
		},
		{
			name: "unexpected status",
			step: robotsBoundaryIntegrationStep{
				response: robotsBoundaryIntegrationResponse(
					http.StatusMultipleChoices,
					"",
				),
			},
			wantErr: errUnexpectedStatus,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := &robotsBoundaryIntegrationGetter{
				steps: []robotsBoundaryIntegrationStep{
					test.step,
				},
			}

			policy, err := obtainPolicy(
				context.Background(),
				initial,
				getter,
			)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf(
						"obtainPolicy() error = %v, want %v",
						err,
						test.wantErr,
					)
				}
			} else if err != nil {
				t.Fatalf(
					"obtainPolicy() error = %v, want nil",
					err,
				)
			}

			if got := policy.Allowed(private); got != test.allowed {
				t.Errorf(
					"Policy.Allowed() = %t, want %t",
					got,
					test.allowed,
				)
			}
		})
	}

	t.Run("body read failure", func(t *testing.T) {
		body := &robotsBoundaryIntegrationBody{
			reader:  strings.NewReader("unused"),
			readErr: errRobotsBoundaryIntegrationRead,
		}
		getter := &robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					response: &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       body,
					},
				},
			},
		}

		policy, err := obtainPolicy(
			context.Background(),
			initial,
			getter,
		)
		if !errors.Is(
			err,
			errRobotsBoundaryIntegrationRead,
		) ||
			policy.Allowed(private) ||
			!body.closed {
			t.Errorf(
				"obtainPolicy() error = %v, allowed=%t, closed=%t",
				err,
				policy.Allowed(private),
				body.closed,
			)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		body := &robotsBoundaryIntegrationBody{
			reader: bytes.NewReader(
				bytes.Repeat(
					[]byte("x"),
					MaxBodySize+1,
				),
			),
		}
		getter := &robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					response: &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       body,
					},
				},
			},
		}

		policy, err := obtainPolicy(
			context.Background(),
			initial,
			getter,
		)
		if !errors.Is(err, errBodyTooLarge) ||
			policy.Allowed(private) ||
			!body.closed {
			t.Errorf(
				"obtainPolicy() error = %v, allowed=%t, closed=%t",
				err,
				policy.Allowed(private),
				body.closed,
			)
		}
	})
}

func TestRobotsBoundaryIntegrationObtainPolicyRedirects(
	t *testing.T,
) {
	initial := robotsBoundaryIntegrationOrigin(
		t,
		"https://example.com",
	)
	private := robotsBoundaryIntegrationTarget(
		t,
		"https://example.com/private",
	)

	for _, test := range []struct {
		name     string
		location string
	}{
		{
			name: "missing location",
		},
		{
			name:     "malformed location",
			location: "%zz",
		},
		{
			name: "credential-bearing location",
			location: "https://user:" +
				"secret@example.com/robots.txt",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := robotsBoundaryIntegrationResponse(
				http.StatusFound,
				"redirect",
			)
			if test.location != "" {
				response.Header.Set(
					"Location",
					test.location,
				)
			}

			policy, err := obtainPolicy(
				context.Background(),
				initial,
				&robotsBoundaryIntegrationGetter{
					steps: []robotsBoundaryIntegrationStep{
						{
							response: response,
						},
					},
				},
			)
			if !errors.Is(err, errInvalidRedirect) ||
				policy.Allowed(private) {
				t.Errorf(
					"obtainPolicy() error = %v, allowed=%t",
					err,
					policy.Allowed(private),
				)
			}
		})
	}

	t.Run("same-origin redirect", func(t *testing.T) {
		redirect := robotsBoundaryIntegrationResponse(
			http.StatusTemporaryRedirect,
			"redirect",
		)
		redirect.Header.Set(
			"Location",
			"/policy#private",
		)
		getter := &robotsBoundaryIntegrationGetter{
			steps: []robotsBoundaryIntegrationStep{
				{
					response: redirect,
				},
				{
					response: robotsBoundaryIntegrationResponse(
						http.StatusOK,
						"User-agent: Joshternet-Joshbot\n"+
							"Disallow: /private\n",
					),
				},
			},
		}

		policy, err := obtainPolicy(
			context.Background(),
			initial,
			getter,
		)
		if err != nil {
			t.Fatalf(
				"obtainPolicy() error = %v",
				err,
			)
		}

		if policy.Allowed(private) {
			t.Error(
				"Policy.Allowed(private) = true, want false",
			)
		}

		if len(getter.targets) != 2 ||
			getter.targets[1] !=
				"https://example.com/policy" {
			t.Errorf(
				"redirect targets = %#v",
				getter.targets,
			)
		}
	})

	t.Run("redirect limit", func(t *testing.T) {
		steps := make(
			[]robotsBoundaryIntegrationStep,
			0,
			maxRedirects+1,
		)

		for index := 0; index < maxRedirects+1; index++ {
			response := robotsBoundaryIntegrationResponse(
				http.StatusFound,
				"redirect",
			)
			response.Header.Set(
				"Location",
				"/redirect-"+string(
					rune('a'+index),
				),
			)

			steps = append(
				steps,
				robotsBoundaryIntegrationStep{
					response: response,
				},
			)
		}

		getter := &robotsBoundaryIntegrationGetter{
			steps: steps,
		}
		policy, err := obtainPolicy(
			context.Background(),
			initial,
			getter,
		)

		if !errors.Is(err, errRedirectLimit) ||
			policy.Allowed(private) {
			t.Errorf(
				"obtainPolicy() error = %v, allowed=%t",
				err,
				policy.Allowed(private),
			)
		}

		if len(getter.targets) != maxRedirects+1 {
			t.Errorf(
				"redirect attempts = %d, want %d",
				len(getter.targets),
				maxRedirects+1,
			)
		}
	})
}

func TestRobotsBoundaryIntegrationSchedulerBoundaries(
	t *testing.T,
) {
	start := time.Date(
		2026,
		time.September,
		19,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	clock := &robotsBoundaryIntegrationClock{
		current: start,
	}
	waiter := &robotsBoundaryIntegrationWaiter{
		clock: clock,
	}
	scheduler := newOriginRequestScheduler(
		clock.Now,
		waiter,
		time.Second,
	)
	target := robotsBoundaryIntegrationTarget(
		t,
		"https://example.com/page",
	)

	if err := scheduler.wait(
		context.Background(),
		nil,
	); !errors.Is(err, errInvalidHTTPTarget) {
		t.Errorf(
			"scheduler.wait(nil) error = %v, want %v",
			err,
			errInvalidHTTPTarget,
		)
	}

	if err := scheduler.wait(
		context.Background(),
		&url.URL{
			Scheme: "file",
			Path:   "/page",
		},
	); !errors.Is(err, errInvalidHTTPTarget) {
		t.Errorf(
			"scheduler.wait(invalid) error = %v, want %v",
			err,
			errInvalidHTTPTarget,
		)
	}

	negative := newOriginRequestScheduler(
		clock.Now,
		waiter,
		-time.Second,
	)
	if err := negative.wait(
		context.Background(),
		target,
	); !errors.Is(err, errInvalidRequestDelay) {
		t.Errorf(
			"scheduler.wait(negative) error = %v, want %v",
			err,
			errInvalidRequestDelay,
		)
	}

	if err := scheduler.wait(
		context.Background(),
		target,
	); err != nil {
		t.Fatalf(
			"first scheduler.wait() error = %v",
			err,
		)
	}

	if err := scheduler.wait(
		context.Background(),
		target,
	); err != nil {
		t.Fatalf(
			"second scheduler.wait() error = %v",
			err,
		)
	}

	if len(waiter.durations) != 1 ||
		waiter.durations[0] != time.Second {
		t.Errorf(
			"wait durations = %#v, want [1s]",
			waiter.durations,
		)
	}

	source := robotsBoundaryIntegrationOrigin(
		t,
		"https://example.com",
	)
	scheduler.setCrawlDelay(
		source,
		3*time.Second,
	)

	if err := scheduler.wait(
		context.Background(),
		target,
	); err != nil {
		t.Fatalf(
			"crawl-delay scheduler.wait() error = %v",
			err,
		)
	}

	if len(waiter.durations) != 2 ||
		waiter.durations[1] != 3*time.Second {
		t.Errorf(
			"wait durations = %#v, want [1s 3s]",
			waiter.durations,
		)
	}

	waiter.err = errRobotsBoundaryIntegrationWait
	if err := scheduler.wait(
		context.Background(),
		target,
	); !errors.Is(
		err,
		errRobotsBoundaryIntegrationWait,
	) {
		t.Errorf(
			"scheduler wait failure = %v, want %v",
			err,
			errRobotsBoundaryIntegrationWait,
		)
	}

	underlying := &robotsBoundaryIntegrationGetter{
		steps: []robotsBoundaryIntegrationStep{
			{
				response: robotsBoundaryIntegrationResponse(
					http.StatusOK,
					"protected",
				),
			},
		},
	}
	freshClock := &robotsBoundaryIntegrationClock{
		current: start,
	}
	freshWaiter := &robotsBoundaryIntegrationWaiter{
		clock: freshClock,
	}
	scheduled := scheduledHopGetter{
		getter: underlying,
		scheduler: newOriginRequestScheduler(
			freshClock.Now,
			freshWaiter,
			0,
		),
	}

	response, err := scheduled.get(
		context.Background(),
		target,
	)
	if err != nil {
		t.Fatalf(
			"scheduled get error = %v",
			err,
		)
	}
	_ = response.Body.Close()

	canceled, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := (timerRequestDelayWaiter{}).Wait(
		canceled,
		time.Hour,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"timer waiter canceled error = %v, want context.Canceled",
			err,
		)
	}

	if err := (timerRequestDelayWaiter{}).Wait(
		context.Background(),
		time.Millisecond,
	); err != nil {
		t.Errorf(
			"timer waiter elapsed error = %v, want nil",
			err,
		)
	}
}

func robotsBoundaryIntegrationResponse(
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body: io.NopCloser(
			strings.NewReader(body),
		),
	}
}

func robotsBoundaryIntegrationTarget(
	t *testing.T,
	raw string,
) *url.URL {
	t.Helper()

	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf(
			"url.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return target
}

func robotsBoundaryIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return source
}
