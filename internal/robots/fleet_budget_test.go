package robots

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeFleetBudget struct {
	waits   []time.Duration
	err     error
	pending []int64
}

func (budget *fakeFleetBudget) Acquire(
	_ context.Context,
	pending int64,
) (time.Duration, error) {
	budget.pending = append(budget.pending, pending)
	if budget.err != nil {
		return 0, budget.err
	}
	if len(budget.waits) == 0 {
		return 0, nil
	}

	wait := budget.waits[0]
	budget.waits = budget.waits[1:]

	return wait, nil
}

type spentFleetBudgetError struct {
	delay time.Duration
}

func (err *spentFleetBudgetError) Error() string {
	return "spent"
}

func (err *spentFleetBudgetError) FleetByteBudgetDelay() time.Duration {
	return err.delay
}

func TestFleetBudgetGate(t *testing.T) {
	target := mustTarget(t, "https://example.com/page")
	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}

	t.Run("refusal happens before dial", func(t *testing.T) {
		dialed := false
		getter := &guardedHTTP{
			resolver: resolver,
			dialer: &recordingDialer{dial: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("dialed")
			}},
			config: DefaultHTTPConfig(),
			budget: &fakeFleetBudget{err: &spentFleetBudgetError{delay: 3 * time.Second}},
		}

		_, err := getter.get(context.Background(), target)
		delay, ok := FleetByteBudgetDelay(err)
		if !errors.Is(err, ErrFleetByteBudget) || !ok || delay != 3*time.Second || dialed {
			t.Fatalf("get() = %v dialed=%v, want the byte budget before dial", err, dialed)
		}
	})

	t.Run("wait happens before dial", func(t *testing.T) {
		dialed := false
		started := time.Now()
		getter := &guardedHTTP{
			resolver: resolver,
			dialer: &recordingDialer{dial: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("dialed")
			}},
			config: DefaultHTTPConfig(),
			budget: &fakeFleetBudget{waits: []time.Duration{20 * time.Millisecond}},
		}

		_, err := getter.get(context.Background(), target)
		if !dialed || time.Since(started) < 20*time.Millisecond {
			t.Fatalf("dialed=%v elapsed=%s error=%v", dialed, time.Since(started), err)
		}
	})

	t.Run("cancellation spends the slot without dialing", func(t *testing.T) {
		dialed := false
		getter := &guardedHTTP{
			resolver: resolver,
			dialer: &recordingDialer{dial: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("dialed")
			}},
			config: DefaultHTTPConfig(),
			budget: &fakeFleetBudget{waits: []time.Duration{time.Hour}},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := getter.get(ctx, target)
		if !errors.Is(err, context.DeadlineExceeded) || dialed {
			t.Fatalf("get() = %v dialed=%v, want deadline before dial", err, dialed)
		}
	})

	t.Run("pending body bytes flush on the next acquire", func(t *testing.T) {
		budget := &fakeFleetBudget{}
		getter := &guardedHTTP{budget: budget}
		body := &fleetBudgetBody{
			ReadCloser: io.NopCloser(strings.NewReader("abcd")),
			add:        getter.addPending,
		}
		if _, err := io.ReadAll(body); err != nil {
			t.Fatal(err)
		}
		if err := getter.reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(&fleetBudgetBody{
			ReadCloser: io.NopCloser(strings.NewReader("ef")),
			add:        getter.addPending,
		}); err != nil {
			t.Fatal(err)
		}
		if err := getter.reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(budget.pending) != 2 || budget.pending[0] != 4 || budget.pending[1] != 2 {
			t.Fatalf("pending = %v, want [4 2]", budget.pending)
		}

		getter.addPending(0)
		getter.addPending(-1)
		getter.pendingBytes = 1
		getter.clearPending(5)
		if getter.pendingBytes != 0 {
			t.Fatalf("pending = %d, want 0", getter.pendingBytes)
		}
	})

	t.Run("database failure keeps pending bytes", func(t *testing.T) {
		budget := &fakeFleetBudget{err: errors.New("database")}
		getter := &guardedHTTP{budget: budget}
		getter.addPending(4)
		err := getter.reserve(context.Background())
		if err == nil || getter.pendingBytes != 4 {
			t.Fatalf("reserve() = %v pending=%d, want the database error and pending bytes", err, getter.pendingBytes)
		}
	})

	t.Run("nil budget does not pace", func(t *testing.T) {
		var getter *guardedHTTP
		getter.addPending(3)
		if err := getter.reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
		getter = &guardedHTTP{}
		if err := getter.reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("checker returns the budget error unwrapped", func(t *testing.T) {
		budgetErr := &FleetByteBudgetError{RetryAfter: time.Second}
		if budgetErr.Error() != ErrFleetByteBudget.Error() {
			t.Fatalf(
				"Error() = %q, want %q",
				budgetErr.Error(),
				ErrFleetByteBudget.Error(),
			)
		}
		checker := newCheckerWithRequestDelay(
			hopGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, budgetErr
			}),
			time.Now,
			0,
			timerRequestDelayWaiter{},
		)
		_, err := checker.Get(context.Background(), target)
		if !errors.Is(err, ErrFleetByteBudget) {
			t.Fatalf("Get() error = %v, want the fleet byte budget", err)
		}

		var empty *FleetByteBudgetError
		if empty.FleetByteBudgetDelay() != 0 {
			t.Fatal("nil fleet byte budget delay is not zero")
		}
		if _, ok := FleetByteBudgetDelay(errors.New("other")); ok {
			t.Fatal("unrelated error reported a fleet byte budget delay")
		}
	})

	t.Run("checker returns the budget error after robots is cached", func(t *testing.T) {
		budgetErr := &FleetByteBudgetError{RetryAfter: time.Second}
		pageFetches := 0
		checker := newCheckerWithRequestDelay(
			hopGetterFunc(func(
				_ context.Context,
				requestURL *url.URL,
			) (*http.Response, error) {
				if strings.HasSuffix(requestURL.Path, "/robots.txt") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(
							strings.NewReader(
								"User-agent: *\nAllow: /\n",
							),
						),
						Header: make(http.Header),
					}, nil
				}

				pageFetches++
				if pageFetches > 1 {
					return nil, budgetErr
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(
						strings.NewReader("ok"),
					),
					Header: make(http.Header),
				}, nil
			}),
			time.Now,
			0,
			timerRequestDelayWaiter{},
		)

		first, err := checker.Get(context.Background(), target)
		if err != nil {
			t.Fatalf("Get() error = %v, want nil", err)
		}
		_ = first.Body.Close()

		_, err = checker.Get(context.Background(), target)
		if !errors.Is(err, ErrFleetByteBudget) {
			t.Fatalf(
				"Get() error = %v, want the fleet byte budget",
				err,
			)
		}
	})

	t.Run("successful response wraps the fleet budget body", func(t *testing.T) {
		budget := &fakeFleetBudget{}
		dialer := &recordingDialer{
			dial: func(context.Context, string, string) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					buffer := make([]byte, 1024)
					_, _ = server.Read(buffer)
					_, _ = io.WriteString(
						server,
						"HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nbody",
					)
				}()
				return client, nil
			},
		}
		getter := &guardedHTTP{
			resolver: resolver,
			dialer:   dialer,
			config:   DefaultHTTPConfig(),
			budget:   budget,
		}

		response, err := getter.get(
			context.Background(),
			mustTarget(t, "http://example.com/page"),
		)
		if err != nil {
			t.Fatalf("get() error = %v", err)
		}
		defer response.Body.Close()

		if _, ok := response.Body.(*fleetBudgetBody); !ok {
			t.Fatalf(
				"response body type = %T, want *fleetBudgetBody",
				response.Body,
			)
		}
		payload, err := io.ReadAll(response.Body)
		if err != nil || string(payload) != "body" {
			t.Fatalf("body = %q, %v", payload, err)
		}
	})
}

type hopGetterFunc func(context.Context, *url.URL) (*http.Response, error)

func (getter hopGetterFunc) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter(ctx, target)
}
