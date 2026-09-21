package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/store"
)

type workerFailureIntegrationQueue struct {
	claim    func(context.Context, string) (store.Lease, bool, error)
	renew    func(context.Context, store.Lease) (store.Lease, error)
	complete func(
		context.Context,
		store.Lease,
		declaration.Result,
		time.Duration,
	) error
	pause func(context.Context) (bool, error)
}

func (queue *workerFailureIntegrationQueue) Claim(
	ctx context.Context,
	workerID string,
) (store.Lease, bool, error) {
	if queue.claim == nil {
		return store.Lease{}, false, nil
	}

	return queue.claim(ctx, workerID)
}

func (queue *workerFailureIntegrationQueue) Renew(
	ctx context.Context,
	lease store.Lease,
) (store.Lease, error) {
	if queue.renew == nil {
		renewed := lease
		renewed.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
		return renewed, nil
	}

	return queue.renew(ctx, lease)
}

func (queue *workerFailureIntegrationQueue) CompleteVerification(
	ctx context.Context,
	lease store.Lease,
	result declaration.Result,
	recheckInterval time.Duration,
) error {
	if queue.complete == nil {
		return nil
	}

	return queue.complete(
		ctx,
		lease,
		result,
		recheckInterval,
	)
}

func (queue *workerFailureIntegrationQueue) VerificationPaused(
	ctx context.Context,
) (bool, error) {
	if queue.pause == nil {
		return false, nil
	}

	return queue.pause(ctx)
}

type workerFailureIntegrationVerifier struct {
	verify func(
		context.Context,
		origin.Origin,
	) (declaration.Result, error)
}

func (verifier workerFailureIntegrationVerifier) Verify(
	ctx context.Context,
	source origin.Origin,
) (declaration.Result, error) {
	if verifier.verify == nil {
		return declaration.Result{
			Origin:  source,
			Outcome: declaration.OutcomeAbsent,
		}, nil
	}

	return verifier.verify(ctx, source)
}

type workerFailureIntegrationWaiter struct {
	calls int
	wait  func(context.Context, time.Duration) error
}

func (waiter *workerFailureIntegrationWaiter) Wait(
	ctx context.Context,
	delay time.Duration,
) error {
	waiter.calls++

	if waiter.wait == nil {
		return nil
	}

	return waiter.wait(ctx, delay)
}

func TestWorkerFailureIntegrationConstructorAndRuntimeValidation(
	t *testing.T,
) {
	queue := &workerFailureIntegrationQueue{}
	verifier := workerFailureIntegrationVerifier{}
	config := workerFailureIntegrationConfig()

	tests := []struct {
		name string
		make func() (*Worker, error)
		want error
	}{
		{
			name: "missing queue",
			make: func() (*Worker, error) {
				return New(
					nil,
					verifier,
					config,
				)
			},
			want: errQueueUnavailable,
		},
		{
			name: "missing verifier",
			make: func() (*Worker, error) {
				return New(
					queue,
					nil,
					config,
				)
			},
			want: errVerifierUnavailable,
		},
		{
			name: "invalid config",
			make: func() (*Worker, error) {
				invalid := config
				invalid.WorkerID = ""

				return New(
					queue,
					verifier,
					invalid,
				)
			},
			want: errInvalidConfig,
		},
		{
			name: "missing waiter",
			make: func() (*Worker, error) {
				return newWorker(
					queue,
					verifier,
					config,
					nil,
					contextTimeoutFactory{},
				)
			},
			want: errWaiterUnavailable,
		},
		{
			name: "missing timeout factory",
			make: func() (*Worker, error) {
				return newWorker(
					queue,
					verifier,
					config,
					timerWaitStrategy{},
					nil,
				)
			},
			want: errTimeoutFactoryUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.make()

			if !errors.Is(err, test.want) {
				t.Fatalf(
					"constructor error = %v, want %v",
					err,
					test.want,
				)
			}

			if got != nil {
				t.Errorf(
					"constructor worker = %#v, want nil",
					got,
				)
			}
		})
	}

	var missing *Worker

	if _, err := missing.RunOnce(
		context.Background(),
	); !errors.Is(err, errWorkerUnavailable) {
		t.Errorf(
			"nil RunOnce() error = %v, want %v",
			err,
			errWorkerUnavailable,
		)
	}

	missing.SetLifecycleObserver(nil)

	valid, err := New(
		queue,
		verifier,
		config,
	)
	if err != nil {
		t.Fatalf(
			"New() error = %v",
			err,
		)
	}

	if _, err := valid.RunOnce(nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Errorf(
			"RunOnce(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := valid.RunOnce(
		canceledContext,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RunOnce(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	runtimeValidation := []struct {
		name   string
		change func(*Worker)
		run    bool
		want   error
	}{
		{
			name: "runtime missing queue",
			change: func(runtime *Worker) {
				runtime.queue = nil
			},
			want: errQueueUnavailable,
		},
		{
			name: "runtime missing verifier",
			change: func(runtime *Worker) {
				runtime.verifier = nil
			},
			want: errVerifierUnavailable,
		},
		{
			name: "runtime invalid config",
			change: func(runtime *Worker) {
				runtime.config.JobTimeout = 0
			},
			want: errInvalidConfig,
		},
		{
			name: "runtime missing timeout factory",
			change: func(runtime *Worker) {
				runtime.timeoutFactory = nil
			},
			want: errTimeoutFactoryUnavailable,
		},
		{
			name: "run requires waiter",
			change: func(runtime *Worker) {
				runtime.waiter = nil
			},
			run:  true,
			want: errWaiterUnavailable,
		},
	}

	for _, test := range runtimeValidation {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := New(
				queue,
				verifier,
				config,
			)
			if err != nil {
				t.Fatalf(
					"New() error = %v",
					err,
				)
			}

			test.change(runtime)

			if test.run {
				err = runtime.Run(
					context.Background(),
				)
			} else {
				_, err = runtime.RunOnce(
					context.Background(),
				)
			}

			if !errors.Is(err, test.want) {
				t.Errorf(
					"runtime validation error = %v, want %v",
					err,
					test.want,
				)
			}
		})
	}
}

func TestWorkerFailureIntegrationRunOnceErrorBoundaries(
	t *testing.T,
) {
	source := workerFailureIntegrationOrigin(
		t,
		"https://example.com",
	)
	lease := workerFailureIntegrationLease(source)

	t.Run("claim failure", func(t *testing.T) {
		expected := errors.New(
			"integration claim failure",
		)

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: func(
					context.Context,
					string,
				) (store.Lease, bool, error) {
					return store.Lease{},
						false,
						expected
				},
			},
			workerFailureIntegrationVerifier{},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(
			context.Background(),
		)

		if worked {
			t.Error(
				"RunOnce() worked = true, want false",
			)
		}

		if !errors.Is(err, expected) ||
			!strings.Contains(
				err.Error(),
				"claim work",
			) {
			t.Errorf(
				"RunOnce() error = %v, want wrapped claim failure",
				err,
			)
		}
	})

	t.Run("pause read failure", func(t *testing.T) {
		expected := errors.New(
			"integration pause failure",
		)

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				pause: func(
					context.Context,
				) (bool, error) {
					return false, expected
				},
			},
			workerFailureIntegrationVerifier{},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(
			context.Background(),
		)

		if worked {
			t.Error(
				"RunOnce() worked = true, want false",
			)
		}

		if !errors.Is(err, expected) {
			t.Errorf(
				"RunOnce() error = %v, want %v",
				err,
				expected,
			)
		}
	})

	t.Run("verification failure", func(t *testing.T) {
		expected := errors.New(
			"integration verification failure",
		)

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
			},
			workerFailureIntegrationVerifier{
				verify: func(
					context.Context,
					origin.Origin,
				) (declaration.Result, error) {
					return declaration.Result{},
						expected
				},
			},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(
			context.Background(),
		)

		if !worked {
			t.Error(
				"RunOnce() worked = false, want true",
			)
		}

		if !errors.Is(err, expected) ||
			!strings.Contains(
				err.Error(),
				"verify origin",
			) {
			t.Errorf(
				"RunOnce() error = %v, want wrapped verification failure",
				err,
			)
		}
	})

	t.Run("parent canceled by verifier error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
			},
			workerFailureIntegrationVerifier{
				verify: func(
					context.Context,
					origin.Origin,
				) (declaration.Result, error) {
					cancel()

					return declaration.Result{},
						errors.New(
							"verification stopped",
						)
				},
			},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(ctx)

		if !worked {
			t.Error(
				"RunOnce() worked = false, want true",
			)
		}

		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"RunOnce() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("parent canceled after successful verification", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
			},
			workerFailureIntegrationVerifier{
				verify: func(
					context.Context,
					origin.Origin,
				) (declaration.Result, error) {
					cancel()

					return declaration.Result{
						Origin:  source,
						Outcome: declaration.OutcomeAbsent,
					}, nil
				},
			},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(ctx)

		if !worked {
			t.Error(
				"RunOnce() worked = false, want true",
			)
		}

		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"RunOnce() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("unavailable result gains default category", func(t *testing.T) {
		calls := 0
		var completed declaration.Result

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
				complete: func(
					_ context.Context,
					_ store.Lease,
					result declaration.Result,
					_ time.Duration,
				) error {
					completed = result
					return nil
				},
			},
			workerFailureIntegrationVerifier{
				verify: func(
					context.Context,
					origin.Origin,
				) (declaration.Result, error) {
					calls++

					return declaration.Result{
						Origin:  source,
						Outcome: declaration.OutcomeUnavailable,
					}, nil
				},
			},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(
			context.Background(),
		)
		if err != nil {
			t.Fatalf(
				"RunOnce() error = %v",
				err,
			)
		}

		if !worked {
			t.Fatal(
				"RunOnce() worked = false, want true",
			)
		}

		if calls != retry.MaxAttemptsPerCycle {
			t.Errorf(
				"verification calls = %d, want %d",
				calls,
				retry.MaxAttemptsPerCycle,
			)
		}

		if completed.FailureCategory !=
			retry.CategoryDeclarationUnavailable {
			t.Errorf(
				"completed category = %q, want %q",
				completed.FailureCategory,
				retry.CategoryDeclarationUnavailable,
			)
		}
	})

	t.Run("parent canceled during completion", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
				complete: func(
					context.Context,
					store.Lease,
					declaration.Result,
					time.Duration,
				) error {
					cancel()
					return errors.New(
						"completion interrupted",
					)
				},
			},
			workerFailureIntegrationVerifier{},
			workerFailureIntegrationConfig(),
		)

		worked, err := runtime.RunOnce(ctx)

		if !worked {
			t.Error(
				"RunOnce() worked = false, want true",
			)
		}

		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"RunOnce() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("completion deadline", func(t *testing.T) {
		config := workerFailureIntegrationConfig()
		config.CompletionGrace = 5 * time.Millisecond

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
				complete: func(
					ctx context.Context,
					_ store.Lease,
					_ declaration.Result,
					_ time.Duration,
				) error {
					<-ctx.Done()
					return ctx.Err()
				},
			},
			workerFailureIntegrationVerifier{},
			config,
		)

		worked, err := runtime.RunOnce(
			context.Background(),
		)

		if !worked {
			t.Error(
				"RunOnce() worked = false, want true",
			)
		}

		if !errors.Is(
			err,
			context.DeadlineExceeded,
		) {
			t.Errorf(
				"RunOnce() error = %v, want context.DeadlineExceeded",
				err,
			)
		}
	})

	t.Run("timed out verification then completion fails", func(t *testing.T) {
		expected := errors.New(
			"integration completion failure",
		)
		config := workerFailureIntegrationConfig()
		config.JobTimeout = 5 * time.Millisecond

		runtime := workerFailureIntegrationWorker(
			t,
			&workerFailureIntegrationQueue{
				claim: workerFailureIntegrationClaim(
					lease,
				),
				complete: func(
					context.Context,
					store.Lease,
					declaration.Result,
					time.Duration,
				) error {
					return expected
				},
			},
			workerFailureIntegrationVerifier{
				verify: func(
					ctx context.Context,
					source origin.Origin,
				) (declaration.Result, error) {
					<-ctx.Done()

					return declaration.Result{
						Origin: source,
					}, ctx.Err()
				},
			},
			config,
		)

		worked, err := runtime.RunOnce(
			context.Background(),
		)

		if !worked {
			t.Error(
				"RunOnce() worked = false, want true",
			)
		}

		if !errors.Is(err, expected) {
			t.Errorf(
				"RunOnce() error = %v, want %v",
				err,
				expected,
			)
		}
	})
}

func TestWorkerFailureIntegrationRunLoopBranches(
	t *testing.T,
) {
	t.Run("context wins after operation failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		waiter := &workerFailureIntegrationWaiter{
			wait: func(
				context.Context,
				time.Duration,
			) error {
				t.Fatal(
					"waiter called after parent cancellation",
				)
				return nil
			},
		}

		runtime, err := newWorker(
			&workerFailureIntegrationQueue{
				claim: func(
					context.Context,
					string,
				) (store.Lease, bool, error) {
					cancel()

					return store.Lease{},
						false,
						errors.New(
							"claim failure",
						)
				},
			},
			workerFailureIntegrationVerifier{},
			workerFailureIntegrationConfig(),
			waiter,
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf(
				"newWorker() error = %v",
				err,
			)
		}

		if err := runtime.Run(ctx); !errors.Is(
			err,
			context.Canceled,
		) {
			t.Errorf(
				"Run() error = %v, want context.Canceled",
				err,
			)
		}

		if waiter.calls != 0 {
			t.Errorf(
				"waiter calls = %d, want 0",
				waiter.calls,
			)
		}
	})

	t.Run("operation failure wait fails", func(t *testing.T) {
		waitFailure := errors.New(
			"integration retry wait failure",
		)
		waiter := &workerFailureIntegrationWaiter{
			wait: func(
				context.Context,
				time.Duration,
			) error {
				return waitFailure
			},
		}

		runtime, err := newWorker(
			&workerFailureIntegrationQueue{
				claim: func(
					context.Context,
					string,
				) (store.Lease, bool, error) {
					return store.Lease{},
						false,
						errors.New(
							"claim failure",
						)
				},
			},
			workerFailureIntegrationVerifier{},
			workerFailureIntegrationConfig(),
			waiter,
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf(
				"newWorker() error = %v",
				err,
			)
		}

		if err := runtime.Run(
			context.Background(),
		); !errors.Is(err, waitFailure) {
			t.Errorf(
				"Run() error = %v, want %v",
				err,
				waitFailure,
			)
		}

		if waiter.calls != 1 {
			t.Errorf(
				"waiter calls = %d, want 1",
				waiter.calls,
			)
		}
	})

	t.Run("successful work immediately polls again", func(t *testing.T) {
		source := workerFailureIntegrationOrigin(
			t,
			"https://example.com",
		)
		lease := workerFailureIntegrationLease(source)
		claims := 0

		idleWaitFailure := errors.New(
			"integration idle wait failure",
		)
		waiter := &workerFailureIntegrationWaiter{
			wait: func(
				context.Context,
				time.Duration,
			) error {
				return idleWaitFailure
			},
		}

		runtime, err := newWorker(
			&workerFailureIntegrationQueue{
				claim: func(
					context.Context,
					string,
				) (store.Lease, bool, error) {
					claims++

					if claims == 1 {
						return lease, true, nil
					}

					return store.Lease{}, false, nil
				},
			},
			workerFailureIntegrationVerifier{},
			workerFailureIntegrationConfig(),
			waiter,
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf(
				"newWorker() error = %v",
				err,
			)
		}

		if err := runtime.Run(
			context.Background(),
		); !errors.Is(err, idleWaitFailure) {
			t.Errorf(
				"Run() error = %v, want %v",
				err,
				idleWaitFailure,
			)
		}

		if claims != 2 {
			t.Errorf(
				"claim calls = %d, want 2",
				claims,
			)
		}

		if waiter.calls != 1 {
			t.Errorf(
				"waiter calls = %d, want 1",
				waiter.calls,
			)
		}
	})

	t.Run("claimed work failure immediately polls again", func(t *testing.T) {
		source := workerFailureIntegrationOrigin(
			t,
			"https://example.com",
		)
		lease := workerFailureIntegrationLease(source)
		claims := 0
		verifyFailure := errors.New(
			"integration claimed verification failure",
		)

		idleWaitFailure := errors.New(
			"integration idle wait failure",
		)
		waiter := &workerFailureIntegrationWaiter{
			wait: func(
				context.Context,
				time.Duration,
			) error {
				return idleWaitFailure
			},
		}

		runtime, err := newWorker(
			&workerFailureIntegrationQueue{
				claim: func(
					context.Context,
					string,
				) (store.Lease, bool, error) {
					claims++

					if claims == 1 {
						return lease, true, nil
					}

					return store.Lease{}, false, nil
				},
			},
			workerFailureIntegrationVerifier{
				verify: func(
					context.Context,
					origin.Origin,
				) (declaration.Result, error) {
					return declaration.Result{},
						verifyFailure
				},
			},
			workerFailureIntegrationConfig(),
			waiter,
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf(
				"newWorker() error = %v",
				err,
			)
		}

		if err := runtime.Run(
			context.Background(),
		); !errors.Is(err, idleWaitFailure) {
			t.Errorf(
				"Run() error = %v, want %v",
				err,
				idleWaitFailure,
			)
		}

		if claims != 2 {
			t.Errorf(
				"claim calls = %d, want 2",
				claims,
			)
		}

		if waiter.calls != 1 {
			t.Errorf(
				"waiter calls = %d, want 1 after claimed-work failure continue",
				waiter.calls,
			)
		}
	})

	t.Run("real timer waiter completes and cancels", func(t *testing.T) {
		waiter := timerWaitStrategy{}

		if err := waiter.Wait(
			context.Background(),
			time.Millisecond,
		); err != nil {
			t.Fatalf(
				"Wait(timer) error = %v",
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
				"Wait(canceled) error = %v, want context.Canceled",
				err,
			)
		}
	})
}

func workerFailureIntegrationWorker(
	t *testing.T,
	queue Queue,
	verifier Verifier,
	config Config,
) *Worker {
	t.Helper()

	runtime, err := New(
		queue,
		verifier,
		config,
	)
	if err != nil {
		t.Fatalf(
			"New() error = %v",
			err,
		)
	}

	return runtime
}

func workerFailureIntegrationClaim(
	lease store.Lease,
) func(
	context.Context,
	string,
) (store.Lease, bool, error) {
	return func(
		context.Context,
		string,
	) (store.Lease, bool, error) {
		return lease, true, nil
	}
}

func workerFailureIntegrationLease(
	source origin.Origin,
) store.Lease {
	claimedAt := time.Now().UTC()

	return store.Lease{
		Origin:     source,
		WorkerID:   "integration-worker",
		Generation: 1,
		ClaimedAt:  claimedAt,
		ExpiresAt:  claimedAt.Add(time.Second),
	}
}

func workerFailureIntegrationConfig() Config {
	return Config{
		WorkerID:        "integration-worker",
		PollInterval:    time.Hour,
		JobTimeout:      20 * time.Millisecond,
		CompletionGrace: 10 * time.Millisecond,
		RecheckInterval: time.Hour,
	}
}

func workerFailureIntegrationOrigin(
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
