package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServiceHeartbeatReplacesProcessGeneration(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	t1 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	first := ServiceHeartbeat{
		Service:       "worker",
		InstanceID:    "worker",
		State:         "running",
		CurrentOrigin: mustStoreOrigin(t, "https://first.example"),
		Message:       "first",
		StartedAt:     t1,
		UpdatedAt:     t1.Add(5 * time.Second),
	}
	if err := observability.UpsertServiceHeartbeat(ctx, first); err != nil {
		t.Fatal(err)
	}

	replacement := ServiceHeartbeat{
		Service:       "worker",
		InstanceID:    "worker",
		State:         "starting",
		CurrentOrigin: mustStoreOrigin(t, "https://second.example"),
		Message:       "second",
		StartedAt:     t2,
		UpdatedAt:     t2,
	}
	if err := observability.UpsertServiceHeartbeat(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	assertStoredHeartbeat(t, ctx, pool, replacement)

	stale := first
	stale.State = "stopping"
	stale.Message = "old-final"
	stale.CurrentOrigin = mustStoreOrigin(t, "https://old.example")
	stale.UpdatedAt = t2.Add(time.Hour)
	if err := observability.UpsertServiceHeartbeat(ctx, stale); err != nil {
		t.Fatal(err)
	}
	assertStoredHeartbeat(t, ctx, pool, replacement)

	current := replacement
	current.State = "running"
	current.Message = "live"
	current.CurrentOrigin = mustStoreOrigin(t, "https://live.example")
	current.UpdatedAt = t2.Add(5 * time.Second)
	if err := observability.UpsertServiceHeartbeat(ctx, current); err != nil {
		t.Fatal(err)
	}
	assertStoredHeartbeat(t, ctx, pool, current)

	sameTime := current
	sameTime.State = "idle"
	sameTime.Message = "same-time"
	if err := observability.UpsertServiceHeartbeat(ctx, sameTime); err != nil {
		t.Fatal(err)
	}
	assertStoredHeartbeat(t, ctx, pool, sameTime)

	backward := current
	backward.State = "failed"
	backward.Message = "backward"
	backward.UpdatedAt = t2
	if err := observability.UpsertServiceHeartbeat(ctx, backward); err != nil {
		t.Fatal(err)
	}
	assertStoredHeartbeat(t, ctx, pool, sameTime)
}

func TestServiceHeartbeatRestartsStayWithinConfiguredSlots(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	var latestWorker ServiceHeartbeat
	var latestDiscovery ServiceHeartbeat
	for generation := 0; generation < 4; generation++ {
		started := base.Add(time.Duration(generation) * time.Minute)
		latestWorker = ServiceHeartbeat{
			Service:    "worker",
			InstanceID: "worker",
			State:      "running",
			Message:    "worker-generation",
			StartedAt:  started,
			UpdatedAt:  started.Add(5 * time.Second),
		}
		latestDiscovery = ServiceHeartbeat{
			Service:    "discovery",
			InstanceID: "discovery",
			State:      "idle",
			Message:    "discovery-generation",
			StartedAt:  started.Add(time.Second),
			UpdatedAt:  started.Add(6 * time.Second),
		}
		if err := observability.UpsertServiceHeartbeat(ctx, latestWorker); err != nil {
			t.Fatal(err)
		}
		if err := observability.UpsertServiceHeartbeat(ctx, latestDiscovery); err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM crawl_service_heartbeats
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("heartbeat rows = %d, want 2", count)
	}
	assertStoredHeartbeat(t, ctx, pool, latestWorker)
	assertStoredHeartbeat(t, ctx, pool, latestDiscovery)
}

func assertStoredHeartbeat(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	want ServiceHeartbeat,
) {
	t.Helper()

	var (
		count                  int
		state, origin, message string
		startedAt, updatedAt   time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM crawl_service_heartbeats
		WHERE service = $1 AND instance_id = $2
	`, want.Service, want.InstanceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows for %s/%s = %d, want 1", want.Service, want.InstanceID, count)
	}
	if err := pool.QueryRow(ctx, `
		SELECT state, current_origin, message, started_at, updated_at
		FROM crawl_service_heartbeats
		WHERE service = $1 AND instance_id = $2
	`, want.Service, want.InstanceID).Scan(
		&state, &origin, &message, &startedAt, &updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if state != want.State || origin != want.CurrentOrigin.String() || message != want.Message ||
		!startedAt.Equal(want.StartedAt) || !updatedAt.Equal(want.UpdatedAt) {
		t.Fatalf(
			"heartbeat = state %q origin %q message %q started %s updated %s, want state %q origin %q message %q started %s updated %s",
			state, origin, message, startedAt, updatedAt,
			want.State, want.CurrentOrigin.String(), want.Message, want.StartedAt, want.UpdatedAt,
		)
	}
}
