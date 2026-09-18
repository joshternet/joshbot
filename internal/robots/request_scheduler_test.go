package robots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestCheckerEnforcesEffectivePerOriginRequestDelay(
	t *testing.T,
) {
	tests := []struct {
		name       string
		minimum    time.Duration
		robotsBody string
		wantDelay  time.Duration
	}{
		{
			name:    "configured minimum wins",
			minimum: 2 * time.Second,
			robotsBody: "User-agent: Joshternet-Joshbot\n" +
				"Crawl-delay: 1\n" +
				"Allow: /\n",
			wantDelay: 2 * time.Second,
		},
		{
			name:    "robots crawl delay wins",
			minimum: time.Second,
			robotsBody: "User-agent: Joshternet-Joshbot\n" +
				"Crawl-delay: 3\n" +
				"Allow: /\n",
			wantDelay: 3 * time.Second,
		},
		{
			name:    "configured minimum applies without crawl delay",
			minimum: 1500 * time.Millisecond,
			robotsBody: "User-agent: Joshternet-Joshbot\n" +
				"Allow: /\n",
			wantDelay: 1500 * time.Millisecond,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := time.Date(
				2026,
				time.September,
				18,
				0,
				0,
				0,
				0,
				time.UTC,
			)

			clock := &requestDelayTestClock{
				current: start,
			}
			waiter := &requestDelayRecordingWaiter{
				clock: clock,
			}
			getter := &requestDelayTestGetter{
				clock: clock,
				respond: func(
					target *url.URL,
				) (*http.Response, error) {
					if target.Path == "/robots.txt" {
						return requestDelayResponse(
							http.StatusOK,
							test.robotsBody,
						), nil
					}

					return requestDelayResponse(
						http.StatusOK,
						"protected",
					), nil
				},
			}

			checker := newCheckerWithRequestDelay(
				getter,
				clock.Now,
				test.minimum,
				waiter,
			)

			for _, path := range []string{
				"/first",
				"/second",
			} {
				response, err := checker.Get(
					context.Background(),
					requestDelayTarget(
						t,
						"https://example.com"+path,
					),
				)
				if err != nil {
					t.Fatalf(
						"Checker.Get(%q) error = %v, want nil",
						path,
						err,
					)
				}

				if err := response.Body.Close(); err != nil {
					t.Fatalf(
						"close response body: %v",
						err,
					)
				}
			}

			wantTargets := []string{
				"https://example.com/robots.txt",
				"https://example.com/first",
				"https://example.com/second",
			}
			if got := requestDelayTargets(
				getter.requests,
			); !slices.Equal(got, wantTargets) {
				t.Fatalf(
					"HTTP targets = %v, want %v",
					got,
					wantTargets,
				)
			}

			wantOffsets := []time.Duration{
				0,
				test.wantDelay,
				2 * test.wantDelay,
			}
			assertRequestDelayOffsets(
				t,
				start,
				getter.requests,
				wantOffsets,
			)

			wantWaits := []time.Duration{
				test.wantDelay,
				test.wantDelay,
			}
			if !slices.Equal(
				waiter.durations,
				wantWaits,
			) {
				t.Errorf(
					"wait durations = %v, want %v",
					waiter.durations,
					wantWaits,
				)
			}
		})
	}
}

func TestCheckerRejectsMalformedApplicableCrawlDelay(
	t *testing.T,
) {
	clock := &requestDelayTestClock{
		current: time.Date(
			2026,
			time.September,
			18,
			0,
			0,
			0,
			0,
			time.UTC,
		),
	}
	waiter := &requestDelayRecordingWaiter{
		clock: clock,
	}
	getter := &requestDelayTestGetter{
		clock: clock,
		respond: func(
			target *url.URL,
		) (*http.Response, error) {
			if target.Path == "/robots.txt" {
				return requestDelayResponse(
					http.StatusOK,
					"User-agent: Joshternet-Joshbot\n"+
						"Crawl-delay: garbage\n"+
						"Allow: /\n",
				), nil
			}

			return requestDelayResponse(
				http.StatusOK,
				"must not be requested",
			), nil
		},
	}

	checker := newCheckerWithRequestDelay(
		getter,
		clock.Now,
		time.Second,
		waiter,
	)

	response, err := checker.Get(
		context.Background(),
		requestDelayTarget(
			t,
			"https://example.com/private",
		),
	)
	if response != nil {
		_ = response.Body.Close()
		t.Fatal(
			"Checker.Get() response is non-nil for malformed Crawl-delay",
		)
	}

	if !errors.Is(err, ErrTemporary) {
		t.Fatalf(
			"Checker.Get() error = %v, want ErrTemporary",
			err,
		)
	}

	if !errors.Is(err, ErrInvalidCrawlDelay) {
		t.Fatalf(
			"Checker.Get() error = %v, want ErrInvalidCrawlDelay",
			err,
		)
	}

	wantTargets := []string{
		"https://example.com/robots.txt",
	}
	if got := requestDelayTargets(
		getter.requests,
	); !slices.Equal(got, wantTargets) {
		t.Errorf(
			"HTTP targets = %v, want %v",
			got,
			wantTargets,
		)
	}
}

func TestCheckerSchedulesSameOriginRobotsRedirect(
	t *testing.T,
) {
	start := time.Date(
		2026,
		time.September,
		18,
		0,
		0,
		0,
		0,
		time.UTC,
	)

	clock := &requestDelayTestClock{
		current: start,
	}
	waiter := &requestDelayRecordingWaiter{
		clock: clock,
	}
	getter := &requestDelayTestGetter{
		clock: clock,
		respond: func(
			target *url.URL,
		) (*http.Response, error) {
			switch target.String() {
			case "https://example.com/robots.txt":
				return requestDelayRedirect(
					"/robots-v2.txt",
				), nil
			case "https://example.com/robots-v2.txt":
				return requestDelayResponse(
					http.StatusOK,
					"User-agent: Joshternet-Joshbot\n"+
						"Crawl-delay: 3\n"+
						"Allow: /\n",
				), nil
			case "https://example.com/page":
				return requestDelayResponse(
					http.StatusOK,
					"page",
				), nil
			default:
				return nil, fmt.Errorf(
					"unexpected target %s",
					target,
				)
			}
		},
	}

	checker := newCheckerWithRequestDelay(
		getter,
		clock.Now,
		2*time.Second,
		waiter,
	)

	response, err := checker.Get(
		context.Background(),
		requestDelayTarget(
			t,
			"https://example.com/page",
		),
	)
	if err != nil {
		t.Fatalf(
			"Checker.Get() error = %v, want nil",
			err,
		)
	}
	_ = response.Body.Close()

	wantTargets := []string{
		"https://example.com/robots.txt",
		"https://example.com/robots-v2.txt",
		"https://example.com/page",
	}
	if got := requestDelayTargets(
		getter.requests,
	); !slices.Equal(got, wantTargets) {
		t.Fatalf(
			"HTTP targets = %v, want %v",
			got,
			wantTargets,
		)
	}

	assertRequestDelayOffsets(
		t,
		start,
		getter.requests,
		[]time.Duration{
			0,
			2 * time.Second,
			5 * time.Second,
		},
	)

	if want := []time.Duration{
		2 * time.Second,
		3 * time.Second,
	}; !slices.Equal(waiter.durations, want) {
		t.Errorf(
			"wait durations = %v, want %v",
			waiter.durations,
			want,
		)
	}
}

func TestCheckerUsesIndependentDestinationOriginSchedule(
	t *testing.T,
) {
	start := time.Date(
		2026,
		time.September,
		18,
		0,
		0,
		0,
		0,
		time.UTC,
	)

	clock := &requestDelayTestClock{
		current: start,
	}
	waiter := &requestDelayRecordingWaiter{
		clock: clock,
	}

	policyRequests := 0

	getter := &requestDelayTestGetter{
		clock: clock,
		respond: func(
			target *url.URL,
		) (*http.Response, error) {
			switch target.String() {
			case "https://source.example/robots.txt":
				return requestDelayRedirect(
					"https://policy.example/robots.txt",
				), nil

			case "https://policy.example/robots.txt":
				policyRequests++

				crawlDelay := "3"
				if policyRequests == 2 {
					crawlDelay = "5"
				}

				return requestDelayResponse(
					http.StatusOK,
					"User-agent: Joshternet-Joshbot\n"+
						"Crawl-delay: "+crawlDelay+"\n"+
						"Allow: /\n",
				), nil

			case "https://source.example/page",
				"https://policy.example/page":
				return requestDelayResponse(
					http.StatusOK,
					"page",
				), nil

			default:
				return nil, fmt.Errorf(
					"unexpected target %s",
					target,
				)
			}
		},
	}

	checker := newCheckerWithRequestDelay(
		getter,
		clock.Now,
		2*time.Second,
		waiter,
	)

	first, err := checker.Get(
		context.Background(),
		requestDelayTarget(
			t,
			"https://source.example/page",
		),
	)
	if err != nil {
		t.Fatalf(
			"source Checker.Get() error = %v, want nil",
			err,
		)
	}
	_ = first.Body.Close()

	second, err := checker.Get(
		context.Background(),
		requestDelayTarget(
			t,
			"https://policy.example/page",
		),
	)
	if err != nil {
		t.Fatalf(
			"destination Checker.Get() error = %v, want nil",
			err,
		)
	}
	_ = second.Body.Close()

	wantTargets := []string{
		"https://source.example/robots.txt",
		"https://policy.example/robots.txt",
		"https://source.example/page",
		"https://policy.example/robots.txt",
		"https://policy.example/page",
	}
	if got := requestDelayTargets(
		getter.requests,
	); !slices.Equal(got, wantTargets) {
		t.Fatalf(
			"HTTP targets = %v, want %v",
			got,
			wantTargets,
		)
	}

	assertRequestDelayOffsets(
		t,
		start,
		getter.requests,
		[]time.Duration{
			0,
			0,
			3 * time.Second,
			3 * time.Second,
			8 * time.Second,
		},
	)

	if want := []time.Duration{
		3 * time.Second,
		5 * time.Second,
	}; !slices.Equal(waiter.durations, want) {
		t.Errorf(
			"wait durations = %v, want %v",
			waiter.durations,
			want,
		)
	}
}

func TestOriginRequestSchedulerUsesActualRequestStart(
	t *testing.T,
) {
	start := time.Date(
		2026,
		time.September,
		18,
		0,
		0,
		0,
		0,
		time.UTC,
	)

	clock := &requestDelayTestClock{
		current: start,
	}
	waiter := &requestDelayRecordingWaiter{
		clock:     clock,
		overshoot: 500 * time.Millisecond,
	}

	scheduler := newOriginRequestScheduler(
		clock.Now,
		waiter,
		time.Second,
	)

	target := requestDelayTarget(
		t,
		"https://example.com/page",
	)

	if err := scheduler.wait(
		context.Background(),
		target,
	); err != nil {
		t.Fatalf(
			"first wait error = %v, want nil",
			err,
		)
	}

	if err := scheduler.wait(
		context.Background(),
		target,
	); err != nil {
		t.Fatalf(
			"second wait error = %v, want nil",
			err,
		)
	}

	if err := scheduler.wait(
		context.Background(),
		target,
	); err != nil {
		t.Fatalf(
			"third wait error = %v, want nil",
			err,
		)
	}

	wantWaits := []time.Duration{
		time.Second,
		time.Second,
	}
	if !slices.Equal(
		waiter.durations,
		wantWaits,
	) {
		t.Errorf(
			"wait durations = %v, want %v",
			waiter.durations,
			wantWaits,
		)
	}

	if got := clock.Now().Sub(start); got != 3*time.Second {
		t.Errorf(
			"clock offset = %v, want 3s",
			got,
		)
	}
}

func TestOriginRequestSchedulerRejectsInvalidTargetsAndDelay(
	t *testing.T,
) {
	clock := &requestDelayTestClock{
		current: time.Now().UTC(),
	}
	waiter := &requestDelayRecordingWaiter{
		clock: clock,
	}

	scheduler := newOriginRequestScheduler(
		clock.Now,
		waiter,
		time.Second,
	)

	if err := scheduler.wait(
		context.Background(),
		nil,
	); !errors.Is(err, errInvalidHTTPTarget) {
		t.Errorf(
			"wait(nil) error = %v, want invalid target",
			err,
		)
	}

	if err := scheduler.wait(
		context.Background(),
		&url.URL{
			Scheme: "file",
			Path:   "/tmp/page",
		},
	); !errors.Is(err, errInvalidHTTPTarget) {
		t.Errorf(
			"wait(file URL) error = %v, want invalid target",
			err,
		)
	}

	invalidDelay := newOriginRequestScheduler(
		clock.Now,
		waiter,
		-time.Nanosecond,
	)

	if err := invalidDelay.wait(
		context.Background(),
		requestDelayTarget(
			t,
			"https://example.com/page",
		),
	); !errors.Is(err, errInvalidRequestDelay) {
		t.Errorf(
			"negative minimum error = %v, want invalid delay",
			err,
		)
	}
}

func TestScheduledHopGetterStopsWhenWaitFails(
	t *testing.T,
) {
	clock := &requestDelayTestClock{
		current: time.Date(
			2026,
			time.September,
			18,
			0,
			0,
			0,
			0,
			time.UTC,
		),
	}
	waiter := &requestDelayRecordingWaiter{
		clock: clock,
	}
	raw := &requestDelayTestGetter{
		clock: clock,
		respond: func(
			*url.URL,
		) (*http.Response, error) {
			return requestDelayResponse(
				http.StatusOK,
				"ok",
			), nil
		},
	}

	scheduled := scheduledHopGetter{
		getter: raw,
		scheduler: newOriginRequestScheduler(
			clock.Now,
			waiter,
			time.Second,
		),
	}

	target := requestDelayTarget(
		t,
		"https://example.com/page",
	)

	first, err := scheduled.get(
		context.Background(),
		target,
	)
	if err != nil {
		t.Fatalf(
			"first get error = %v, want nil",
			err,
		)
	}
	_ = first.Body.Close()

	waitFailure := errors.New(
		"test request delay wait failure",
	)
	waiter.err = waitFailure

	second, err := scheduled.get(
		context.Background(),
		target,
	)
	if second != nil {
		_ = second.Body.Close()
		t.Fatal(
			"second get response is non-nil after wait failure",
		)
	}

	if !errors.Is(err, waitFailure) {
		t.Fatalf(
			"second get error = %v, want wait failure",
			err,
		)
	}

	if len(raw.requests) != 1 {
		t.Errorf(
			"raw request count = %d, want 1",
			len(raw.requests),
		)
	}
}

func TestTimerRequestDelayWaiter(
	t *testing.T,
) {
	waiter := timerRequestDelayWaiter{}

	if err := waiter.Wait(
		context.Background(),
		time.Nanosecond,
	); err != nil {
		t.Fatalf(
			"completed timer error = %v, want nil",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := waiter.Wait(
		ctx,
		time.Hour,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"canceled timer error = %v, want context.Canceled",
			err,
		)
	}
}

type requestDelayTestClock struct {
	current time.Time
}

func (clock *requestDelayTestClock) Now() time.Time {
	return clock.current
}

func (clock *requestDelayTestClock) Advance(
	duration time.Duration,
) {
	clock.current = clock.current.Add(
		duration,
	)
}

type requestDelayRecordingWaiter struct {
	clock     *requestDelayTestClock
	durations []time.Duration
	err       error
	overshoot time.Duration
}

func (waiter *requestDelayRecordingWaiter) Wait(
	ctx context.Context,
	duration time.Duration,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if waiter.err != nil {
		return waiter.err
	}

	waiter.durations = append(
		waiter.durations,
		duration,
	)

	waiter.clock.Advance(
		duration + waiter.overshoot,
	)

	return nil
}

type requestDelayRecordedRequest struct {
	target string
	at     time.Time
}

type requestDelayTestGetter struct {
	clock    *requestDelayTestClock
	respond  func(*url.URL) (*http.Response, error)
	requests []requestDelayRecordedRequest
}

func (getter *requestDelayTestGetter) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	getter.requests = append(
		getter.requests,
		requestDelayRecordedRequest{
			target: target.String(),
			at:     getter.clock.Now(),
		},
	)

	return getter.respond(target)
}

func requestDelayResponse(
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body: io.NopCloser(
			&requestDelayReader{
				value: body,
			},
		),
	}
}

func requestDelayRedirect(
	location string,
) *http.Response {
	response := requestDelayResponse(
		http.StatusFound,
		"",
	)
	response.Header.Set(
		"Location",
		location,
	)

	return response
}

type requestDelayReader struct {
	value  string
	offset int
}

func (reader *requestDelayReader) Read(
	buffer []byte,
) (int, error) {
	if reader.offset >= len(reader.value) {
		return 0, io.EOF
	}

	copied := copy(
		buffer,
		reader.value[reader.offset:],
	)
	reader.offset += copied

	return copied, nil
}

func requestDelayTarget(
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

func requestDelayTargets(
	requests []requestDelayRecordedRequest,
) []string {
	targets := make(
		[]string,
		0,
		len(requests),
	)

	for _, request := range requests {
		targets = append(
			targets,
			request.target,
		)
	}

	return targets
}

func assertRequestDelayOffsets(
	t *testing.T,
	start time.Time,
	requests []requestDelayRecordedRequest,
	want []time.Duration,
) {
	t.Helper()

	if len(requests) != len(want) {
		t.Fatalf(
			"request count = %d, want %d",
			len(requests),
			len(want),
		)
	}

	for index, request := range requests {
		got := request.at.Sub(start)
		if got != want[index] {
			t.Errorf(
				"request %d offset = %v, want %v",
				index,
				got,
				want[index],
			)
		}
	}
}
