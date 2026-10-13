package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

const maxServiceHeartbeatMessageBytes = 256

const serviceHeartbeatWriteTimeout = 5 * time.Second

const serviceHeartbeatInterval = 5 * time.Second

type heartbeatStore interface {
	UpsertServiceHeartbeat(context.Context, store.ServiceHeartbeat) error
	PurgeOperationalHistory(context.Context, time.Duration) error
	PurgeCrawlTelemetry(context.Context, time.Duration) (int64, error)
}

type serviceHeartbeat struct {
	store         heartbeatStore
	writeTimeout  time.Duration
	service       string
	instanceID    string
	startedAt     time.Time
	cancel        context.CancelFunc
	done          chan struct{}
	once          sync.Once
	mu            sync.RWMutex
	state         string
	current       origin.Origin
	message       string
	reportError   func(error)
	failureActive bool
	stopErr       error
}

func heartbeatErrorReporter(logger *slog.Logger) func(error) {
	if logger == nil {
		return nil
	}
	return func(err error) {
		logger.Error("service heartbeat write failed", "error", err)
	}
}

func startServiceHeartbeatWithReporter(
	ctx context.Context,
	storage heartbeatStore,
	service string,
	instanceID string,
	interval time.Duration,
	reportError func(error),
) (*serviceHeartbeat, error) {
	startedAt := time.Now().UTC()
	monitor := &serviceHeartbeat{
		writeTimeout: serviceHeartbeatWriteTimeout,
		store:        storage, service: service, instanceID: instanceID,
		startedAt: startedAt, done: make(chan struct{}), state: "starting",
		reportError: reportError,
	}
	if err := monitor.write(ctx, "starting"); err != nil {
		return nil, fmt.Errorf("start service heartbeat: %w", err)
	}
	monitorContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	monitor.cancel = cancel
	go monitor.run(monitorContext, interval)
	return monitor, nil
}

func (heartbeat *serviceHeartbeat) run(ctx context.Context, interval time.Duration) {
	defer close(heartbeat.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = heartbeat.write(ctx, "")
		}
	}
}

func (heartbeat *serviceHeartbeat) stop(ctx context.Context, state string) error {
	if heartbeat == nil {
		return nil
	}
	heartbeat.once.Do(func() {
		heartbeat.cancel()
		<-heartbeat.done
		heartbeat.Observe(state, origin.Origin{}, "")
		heartbeat.stopErr = heartbeat.write(context.WithoutCancel(ctx), "")
	})
	return heartbeat.stopErr
}

func (heartbeat *serviceHeartbeat) write(ctx context.Context, state string) error {
	writeContext, cancel := context.WithTimeout(ctx, heartbeat.writeTimeout)
	defer cancel()
	heartbeat.mu.RLock()
	currentState := heartbeat.state
	currentOrigin := heartbeat.current
	message := heartbeat.message
	heartbeat.mu.RUnlock()
	if state != "" {
		currentState = state
	}
	now := time.Now().UTC()
	err := heartbeat.store.UpsertServiceHeartbeat(writeContext, store.ServiceHeartbeat{
		Service: heartbeat.service, InstanceID: heartbeat.instanceID,
		State: currentState, CurrentOrigin: currentOrigin, Message: message,
		StartedAt: heartbeat.startedAt, UpdatedAt: now,
	})
	heartbeat.recordWriteResult(err)
	return err
}

func (heartbeat *serviceHeartbeat) recordWriteResult(err error) {
	heartbeat.mu.Lock()
	shouldReport := err != nil && !heartbeat.failureActive
	heartbeat.failureActive = err != nil
	reportError := heartbeat.reportError
	heartbeat.mu.Unlock()
	if shouldReport && reportError != nil {
		reportError(fmt.Errorf("service heartbeat persistence: %w", err))
	}
}

func (heartbeat *serviceHeartbeat) Observe(
	state string,
	currentOrigin origin.Origin,
	message string,
) {
	if heartbeat == nil {
		return
	}
	message = truncateUTF8Bytes(
		message,
		maxServiceHeartbeatMessageBytes,
	)
	heartbeat.mu.Lock()
	heartbeat.state = state
	heartbeat.current = currentOrigin
	heartbeat.message = message
	heartbeat.mu.Unlock()
}

// truncateUTF8Bytes shortens s to at most maxBytes without splitting a rune.
func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		if maxBytes <= 0 {
			return ""
		}

		return s
	}

	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}

	return s[:maxBytes]
}
