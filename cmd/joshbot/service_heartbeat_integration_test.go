package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

type serviceHeartbeatIntegrationStore struct {
	mu sync.Mutex

	writes      []store.ServiceHeartbeat
	writeErrors []error
	written     chan struct{}
}

func (storage *serviceHeartbeatIntegrationStore) UpsertServiceHeartbeat(
	_ context.Context,
	heartbeat store.ServiceHeartbeat,
) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()

	storage.writes = append(
		storage.writes,
		heartbeat,
	)

	if storage.written != nil {
		select {
		case storage.written <- struct{}{}:
		default:
		}
	}

	if len(storage.writeErrors) == 0 {
		return nil
	}

	err := storage.writeErrors[0]
	storage.writeErrors = storage.writeErrors[1:]
	return err
}

func (*serviceHeartbeatIntegrationStore) PurgeCrawlTelemetry(
	context.Context,
	time.Duration,
) (int64, error) {
	return 0, nil
}

func TestServiceHeartbeatIntegrationPersistsObservedLifecycle(
	t *testing.T,
) {
	storage := &serviceHeartbeatIntegrationStore{
		written: make(chan struct{}, 16),
	}

	heartbeat, err := startServiceHeartbeatWithInterval(
		context.Background(),
		storage,
		"discovery",
		"integration-discovery",
		10*time.Millisecond,
	)
	if err != nil {
		t.Fatalf(
			"startServiceHeartbeatWithInterval() error = %v",
			err,
		)
	}

	select {
	case <-storage.written:
	case <-time.After(time.Second):
		t.Fatal(
			"initial heartbeat was not persisted",
		)
	}

	current, err := origin.Parse(
		"https://example.com/path?secret=value",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	heartbeat.Observe(
		"running",
		current,
		strings.Repeat("x", 300),
	)

	deadline := time.After(time.Second)
	for {
		select {
		case <-storage.written:
			storage.mu.Lock()
			latest := storage.writes[len(storage.writes)-1]
			storage.mu.Unlock()

			if latest.State == "running" {
				if latest.CurrentOrigin.String() !=
					"https://example.com" {
					t.Errorf(
						"running origin = %q, want https://example.com",
						latest.CurrentOrigin.String(),
					)
				}

				if len(latest.Message) != 256 {
					t.Errorf(
						"running message length = %d, want 256",
						len(latest.Message),
					)
				}

				goto observed
			}

		case <-deadline:
			t.Fatal(
				"running heartbeat was not persisted",
			)
		}
	}

observed:
	if err := heartbeat.stop(
		context.Background(),
		"stopping",
	); err != nil {
		t.Fatalf(
			"heartbeat.stop() error = %v",
			err,
		)
	}

	if err := heartbeat.stop(
		context.Background(),
		"stopping",
	); err != nil {
		t.Fatalf(
			"second heartbeat.stop() error = %v",
			err,
		)
	}

	storage.mu.Lock()
	final := storage.writes[len(storage.writes)-1]
	first := storage.writes[0]
	storage.mu.Unlock()

	if first.State != "starting" ||
		first.Service != "discovery" ||
		first.InstanceID != "integration-discovery" {
		t.Errorf(
			"initial heartbeat = %#v",
			first,
		)
	}

	if final.State != "stopping" ||
		final.CurrentOrigin.String() != "" ||
		final.Message != "" {
		t.Errorf(
			"final heartbeat = %#v",
			final,
		)
	}
}

func TestServiceHeartbeatIntegrationDefaultStartAndNilStop(
	t *testing.T,
) {
	storage := &serviceHeartbeatIntegrationStore{}

	heartbeat, err := startServiceHeartbeat(
		context.Background(),
		storage,
		"worker",
		"integration-worker",
	)
	if err != nil {
		t.Fatalf(
			"startServiceHeartbeat() error = %v",
			err,
		)
	}

	if err := heartbeat.stop(
		context.Background(),
		"idle",
	); err != nil {
		t.Fatalf(
			"heartbeat.stop() error = %v",
			err,
		)
	}

	var missing *serviceHeartbeat

	missing.Observe(
		"running",
		origin.Origin{},
		"ignored",
	)

	if err := missing.stop(
		context.Background(),
		"stopping",
	); err != nil {
		t.Errorf(
			"nil heartbeat stop error = %v",
			err,
		)
	}
}

func TestServiceHeartbeatIntegrationReportsPersistenceFailureOncePerOutage(
	t *testing.T,
) {
	firstFailure := errors.New(
		"first integration heartbeat outage",
	)
	secondFailure := errors.New(
		"second integration heartbeat outage",
	)

	storage := &serviceHeartbeatIntegrationStore{
		writeErrors: []error{
			nil,
			firstFailure,
			firstFailure,
			nil,
			secondFailure,
		},
	}

	var reportedMu sync.Mutex
	var reported []error

	heartbeat, err := startServiceHeartbeatWithReporter(
		context.Background(),
		storage,
		"worker",
		"integration-worker",
		time.Hour,
		func(err error) {
			reportedMu.Lock()
			defer reportedMu.Unlock()

			reported = append(
				reported,
				err,
			)
		},
	)
	if err != nil {
		t.Fatalf(
			"startServiceHeartbeatWithReporter() error = %v",
			err,
		)
	}

	for range 3 {
		_ = heartbeat.write(
			context.Background(),
			"",
		)
	}

	stopErr := heartbeat.stop(
		context.Background(),
		"failed",
	)
	if !errors.Is(
		stopErr,
		secondFailure,
	) {
		t.Fatalf(
			"heartbeat.stop() error = %v, want %v",
			stopErr,
			secondFailure,
		)
	}

	reportedMu.Lock()
	defer reportedMu.Unlock()

	if len(reported) != 2 {
		t.Fatalf(
			"reported failures = %d, want 2",
			len(reported),
		)
	}

	if !errors.Is(
		reported[0],
		firstFailure,
	) || !errors.Is(
		reported[1],
		secondFailure,
	) {
		t.Errorf(
			"reported failures = %#v",
			reported,
		)
	}
}

func TestServiceHeartbeatIntegrationTruncateUTF8Bytes(
	t *testing.T,
) {
	t.Run("maxBytes zero returns empty", func(t *testing.T) {
		if got := truncateUTF8Bytes("hello", 0); got != "" {
			t.Errorf("truncateUTF8Bytes(%q, 0) = %q, want empty", "hello", got)
		}
	})

	t.Run("rune boundary walk", func(t *testing.T) {
		// "café" = c(0x63) a(0x61) f(0x66) é(0xC3 0xA9) = 5 bytes.
		// Truncation at byte 4 lands inside the 2-byte é sequence;
		// the walk backs up to byte 3.
		if got := truncateUTF8Bytes("café", 4); got != "caf" {
			t.Errorf(
				"truncateUTF8Bytes(%q, 4) = %q, want %q",
				"café", got, "caf",
			)
		}
	})

	t.Run("exact fit unchanged", func(t *testing.T) {
		if got := truncateUTF8Bytes("café", 5); got != "café" {
			t.Errorf(
				"truncateUTF8Bytes(%q, 5) = %q, want %q",
				"café", got, "café",
			)
		}
	})
}

func TestServiceHeartbeatIntegrationSlogReporter(
	t *testing.T,
) {
	if heartbeatErrorReporter(nil) != nil {
		t.Fatal(
			"heartbeatErrorReporter(nil) != nil",
		)
	}

	var output bytes.Buffer
	logger := slog.New(
		slog.NewTextHandler(
			&output,
			&slog.HandlerOptions{
				Level: slog.LevelDebug,
			},
		),
	)

	reporter := heartbeatErrorReporter(
		logger,
	)
	if reporter == nil {
		t.Fatal(
			"heartbeatErrorReporter(logger) = nil",
		)
	}

	reporter(
		errors.New(
			"integration persistence failure",
		),
	)

	logged := output.String()

	if !strings.Contains(
		logged,
		"service heartbeat write failed",
	) || !strings.Contains(
		logged,
		"integration persistence failure",
	) {
		t.Errorf(
			"heartbeat log = %q",
			logged,
		)
	}
}

type deadlineHeartbeatStore struct {
	serviceHeartbeatIntegrationStore
	finalContext context.Context
}

func (storage *deadlineHeartbeatStore) UpsertServiceHeartbeat(ctx context.Context, heartbeat store.ServiceHeartbeat) error {
	if heartbeat.State == "stopping" {
		storage.finalContext = ctx
	}
	return storage.serviceHeartbeatIntegrationStore.UpsertServiceHeartbeat(ctx, heartbeat)
}

func TestServiceHeartbeatShutdownHasIndependentDeadline(t *testing.T) {
	storage := &deadlineHeartbeatStore{}
	heartbeat, err := startServiceHeartbeatWithInterval(context.Background(), storage, "discovery", "bounded-shutdown", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := heartbeat.stop(ctx, "stopping"); err != nil {
		t.Fatal(err)
	}
	if storage.finalContext == nil {
		t.Fatal("missing final heartbeat")
	}
	if _, ok := storage.finalContext.Deadline(); !ok {
		t.Fatal("final heartbeat has no deadline")
	}
}

type stalledHeartbeatStore struct {
	serviceHeartbeatIntegrationStore
}

func (storage *stalledHeartbeatStore) UpsertServiceHeartbeat(ctx context.Context, heartbeat store.ServiceHeartbeat) error {
	if heartbeat.State == "starting" {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestServiceHeartbeatShutdownReturnsAfterStalledWrite(t *testing.T) {
	heartbeat, err := startServiceHeartbeatWithInterval(context.Background(), &stalledHeartbeatStore{}, "discovery", "stalled-shutdown", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat.writeTimeout = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- heartbeat.stop(ctx, "stopping") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled shutdown write did not return")
	}
}
