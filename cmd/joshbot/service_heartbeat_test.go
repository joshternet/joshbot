package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

type fakeHeartbeatStore struct {
	mu          sync.Mutex
	writes      []store.ServiceHeartbeat
	writeErr    error
	writeErrors []error
	purgeErr    error
	written     chan struct{}
}

func startServiceHeartbeat(
	ctx context.Context,
	storage heartbeatStore,
	service string,
	instanceID string,
) (*serviceHeartbeat, error) {
	return startServiceHeartbeatWithReporter(
		ctx, storage, service, instanceID, serviceHeartbeatInterval, nil,
	)
}

func startServiceHeartbeatWithInterval(
	ctx context.Context,
	storage heartbeatStore,
	service string,
	instanceID string,
	interval time.Duration,
) (*serviceHeartbeat, error) {
	return startServiceHeartbeatWithReporter(
		ctx, storage, service, instanceID, interval, nil,
	)
}

func (storage *fakeHeartbeatStore) UpsertServiceHeartbeat(
	_ context.Context,
	heartbeat store.ServiceHeartbeat,
) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.writes = append(storage.writes, heartbeat)
	if storage.written != nil {
		storage.written <- struct{}{}
	}
	if len(storage.writeErrors) > 0 {
		err := storage.writeErrors[0]
		storage.writeErrors = storage.writeErrors[1:]
		return err
	}
	return storage.writeErr
}

func (storage *fakeHeartbeatStore) PurgeCrawlTelemetry(
	context.Context,
	time.Duration,
) (int64, error) {
	return 0, storage.purgeErr
}

func TestServiceHeartbeatWritesLifecycle(t *testing.T) {
	storage := &fakeHeartbeatStore{}
	heartbeat, err := startServiceHeartbeatWithInterval(
		context.Background(), storage, "worker", "worker-one", time.Hour,
	)
	if err != nil {
		t.Fatalf("startServiceHeartbeat() error = %v", err)
	}
	if err := heartbeat.write(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err := heartbeat.stop(context.Background(), "stopping"); err != nil {
		t.Fatal(err)
	}
	if err := heartbeat.stop(context.Background(), "stopping"); err != nil {
		t.Fatal(err)
	}

	storage.mu.Lock()
	defer storage.mu.Unlock()
	if len(storage.writes) < 3 {
		t.Fatalf("heartbeat writes = %d, want at least 3", len(storage.writes))
	}
	if storage.writes[0].State != "starting" || storage.writes[len(storage.writes)-1].State != "stopping" {
		t.Errorf("lifecycle = %q ... %q", storage.writes[0].State, storage.writes[len(storage.writes)-1].State)
	}
	if storage.writes[0].InstanceID != "worker-one" {
		t.Errorf("instance = %q", storage.writes[0].InstanceID)
	}
	if storage.writes[0].BuildVersion != buildVersion ||
		storage.writes[0].BuildCommit != buildCommit {
		t.Errorf(
			"build = %q %q, want %q %q",
			storage.writes[0].BuildVersion,
			storage.writes[0].BuildCommit,
			buildVersion,
			buildCommit,
		)
	}
}

func TestServiceHeartbeatHandlesUnavailablePersistence(t *testing.T) {
	want := errors.New("heartbeat unavailable")
	storage := &fakeHeartbeatStore{writeErr: want}
	heartbeat, err := startServiceHeartbeat(
		context.Background(), storage, "discovery", "one",
	)
	if heartbeat != nil || !errors.Is(err, want) {
		t.Errorf("start = %#v, %v, want nil and error", heartbeat, err)
	}
	var missing *serviceHeartbeat
	if err := missing.stop(context.Background(), "stopping"); err != nil {
		t.Errorf("nil heartbeat stop error = %v", err)
	}
}

func TestTruncateUTF8Bytes(t *testing.T) {
	if got := truncateUTF8Bytes("abc", 0); got != "" {
		t.Errorf("truncateUTF8Bytes(maxBytes=0) = %q, want empty", got)
	}
	if got := truncateUTF8Bytes("abc", -1); got != "" {
		t.Errorf("truncateUTF8Bytes(maxBytes=-1) = %q, want empty", got)
	}
	if got := truncateUTF8Bytes("abc", 10); got != "abc" {
		t.Errorf("truncateUTF8Bytes(short) = %q, want abc", got)
	}

	// "á" is two UTF-8 bytes; cutting at one byte must drop the partial rune.
	if got := truncateUTF8Bytes("áx", 1); got != "" {
		t.Errorf("truncateUTF8Bytes(mid-rune) = %q, want empty", got)
	}
	if got := truncateUTF8Bytes("aá", 2); got != "a" {
		t.Errorf("truncateUTF8Bytes(mid-rune) = %q, want a", got)
	}
}

func TestServiceHeartbeatPublishesBoundedLifecycleState(t *testing.T) {
	storage := &fakeHeartbeatStore{}
	heartbeat, err := startServiceHeartbeatWithInterval(
		context.Background(), storage, "discovery", "one", time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := origin.Parse("https://example.com/path?secret=value")
	if err != nil {
		t.Fatal(err)
	}
	multibyte := strings.Repeat("á", 200)
	heartbeat.Observe("running", current, multibyte)
	if err := heartbeat.write(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err := heartbeat.stop(context.Background(), "stopping"); err != nil {
		t.Fatal(err)
	}

	storage.mu.Lock()
	defer storage.mu.Unlock()
	running := storage.writes[len(storage.writes)-2]
	stopped := storage.writes[len(storage.writes)-1]
	wantMessage := multibyte[:maxServiceHeartbeatMessageBytes]
	if running.State != "running" ||
		running.CurrentOrigin.String() != "https://example.com" ||
		running.Message != wantMessage ||
		!utf8.ValidString(running.Message) {
		t.Errorf("running heartbeat = %#v", running)
	}
	if stopped.State != "stopping" || stopped.CurrentOrigin.String() != "" ||
		stopped.Message != "" || !stopped.StartedAt.Equal(running.StartedAt) {
		t.Errorf("stopped heartbeat = %#v", stopped)
	}
}

func TestServiceHeartbeatRateLimitsAndSurfacesWriteFailures(t *testing.T) {
	first := errors.New("first heartbeat outage")
	second := errors.New("second heartbeat outage")
	storage := &fakeHeartbeatStore{writeErrors: []error{
		nil, first, first, nil, second, second,
	}}
	var reported []error
	heartbeat, err := startServiceHeartbeatWithReporter(
		context.Background(), storage, "worker", "one", time.Hour,
		func(err error) {
			reported = append(reported, err)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		_ = heartbeat.write(context.Background(), "")
	}
	stopErr := heartbeat.stop(context.Background(), "failed")
	if !errors.Is(stopErr, second) {
		t.Fatalf("stop error = %v, want second outage", stopErr)
	}
	if len(reported) != 2 ||
		!errors.Is(reported[0], first) ||
		!errors.Is(reported[1], second) {
		t.Errorf("reported errors = %#v", reported)
	}
}

func TestHeartbeatErrorReporterAndNilObserver(t *testing.T) {
	if heartbeatErrorReporter(nil) != nil {
		t.Error("heartbeatErrorReporter(nil) != nil")
	}
	var heartbeat *serviceHeartbeat
	heartbeat.Observe("idle", origin.Origin{}, "")
}

func TestServiceHeartbeatPeriodicWrite(t *testing.T) {
	storage := &fakeHeartbeatStore{written: make(chan struct{}, 4)}
	heartbeat, err := startServiceHeartbeatWithInterval(
		context.Background(),
		storage,
		"worker",
		"periodic",
		time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	<-storage.written
	select {
	case <-storage.written:
	case <-time.After(time.Second):
		t.Fatal("periodic heartbeat was not written")
	}
	if err := heartbeat.stop(context.Background(), "stopping"); err != nil {
		t.Fatal(err)
	}
}
