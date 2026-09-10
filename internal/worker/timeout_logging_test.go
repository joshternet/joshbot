package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestRunOnceLogsCompletedTimeoutWithoutRawOrigin(
	t *testing.T,
) {
	source := mustTimeoutLogOrigin(
		t,
		"https://private-candidate.example",
	)

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
	}

	verifier := &fakeVerifier{
		verify: func(
			ctx context.Context,
			_ origin.Origin,
		) (declaration.Result, error) {
			<-ctx.Done()

			return declaration.Result{}, ctx.Err()
		},
	}

	var output bytes.Buffer
	restore := captureWorkerLogs(t, &output)
	defer restore()

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			if timeoutCalls == 1 {
				return context.WithDeadline(
					ctx,
					time.Unix(0, 0),
				)
			}

			return context.WithCancel(ctx)
		},
	)

	runtime, err := newWorker(
		queue,
		verifier,
		workerTestConfig(),
		timerWaitStrategy{},
		factory,
	)
	if err != nil {
		t.Fatalf(
			"newWorker() error = %v, want nil",
			err,
		)
	}

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v, want nil",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	logged := output.String()

	assertTimeoutLogContains(
		t,
		logged,
		source,
		"completed",
	)

	if strings.Contains(
		logged,
		source.String(),
	) {
		t.Errorf(
			"timeout log contains raw origin %q",
			source.String(),
		)
	}
}

func TestRunOnceLogsAbandonedTimeoutWhenCompletionFails(
	t *testing.T,
) {
	source := mustTimeoutLogOrigin(
		t,
		"https://another-private-candidate.example",
	)
	completeError := errors.New(
		"test completion failure",
	)

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
		completeError: completeError,
	}

	verifier := &fakeVerifier{
		verify: func(
			ctx context.Context,
			_ origin.Origin,
		) (declaration.Result, error) {
			<-ctx.Done()

			return declaration.Result{}, ctx.Err()
		},
	}

	var output bytes.Buffer
	restore := captureWorkerLogs(t, &output)
	defer restore()

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			if timeoutCalls == 1 {
				return context.WithDeadline(
					ctx,
					time.Unix(0, 0),
				)
			}

			return context.WithCancel(ctx)
		},
	)

	runtime, err := newWorker(
		queue,
		verifier,
		workerTestConfig(),
		timerWaitStrategy{},
		factory,
	)
	if err != nil {
		t.Fatalf(
			"newWorker() error = %v, want nil",
			err,
		)
	}

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if !errors.Is(err, completeError) {
		t.Fatalf(
			"RunOnce() error = %v, want completion failure",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	logged := output.String()

	assertTimeoutLogContains(
		t,
		logged,
		source,
		"abandoned",
	)

	if strings.Contains(
		logged,
		source.String(),
	) {
		t.Errorf(
			"timeout log contains raw origin %q",
			source.String(),
		)
	}
}

func TestRunOnceDoesNotLogTimeoutForOrdinaryFailure(
	t *testing.T,
) {
	source := mustTimeoutLogOrigin(
		t,
		"https://ordinary-failure.example",
	)
	verifyError := errors.New(
		"test verification failure",
	)

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
	}

	verifier := &fakeVerifier{
		err: verifyError,
	}

	var output bytes.Buffer
	restore := captureWorkerLogs(t, &output)
	defer restore()

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if !errors.Is(err, verifyError) {
		t.Fatalf(
			"RunOnce() error = %v, want verification failure",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	if strings.Contains(
		output.String(),
		"verification timed out",
	) {
		t.Errorf(
			"ordinary verification failure logged as timeout: %q",
			output.String(),
		)
	}
}

func assertTimeoutLogContains(
	t *testing.T,
	logged string,
	source origin.Origin,
	action string,
) {
	t.Helper()

	sum := sha256.Sum256(
		[]byte(source.String()),
	)
	originID := hex.EncodeToString(
		sum[:8],
	)

	required := []string{
		"verification timed out",
		"origin_id=" + originID,
		"outcome=unavailable",
		"lease_action=" + action,
	}

	for _, value := range required {
		if !strings.Contains(logged, value) {
			t.Errorf(
				"timeout log = %q, want %q",
				logged,
				value,
			)
		}
	}
}

func captureWorkerLogs(
	t *testing.T,
	output *bytes.Buffer,
) func() {
	t.Helper()

	previous := slog.Default()

	slog.SetDefault(
		slog.New(
			slog.NewTextHandler(
				output,
				&slog.HandlerOptions{
					Level: slog.LevelDebug,
				},
			),
		),
	)

	return func() {
		slog.SetDefault(previous)
	}
}

func mustTimeoutLogOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v, want nil",
			raw,
			err,
		)
	}

	return source
}
