package store

import (
	"context"
	"testing"
	"time"
)

func TestQueueRecordsDurableLifecycleEvents(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	queue, err := NewQueue(pool, QueueConfig{
		LeaseDuration: time.Minute, MinOriginInterval: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := mustStoreOrigin(t, "https://events.example")
	if err := queue.Schedule(ctx, source, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "event-worker")
	if err != nil || !found {
		t.Fatalf("Claim() = %+v, %t, %v", lease, found, err)
	}
	if _, err := queue.Renew(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := queue.Reschedule(ctx, lease, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `
		SELECT event FROM verification_queue_events
		WHERE origin = $1 ORDER BY id`, source.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	events := []string{}
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	want := []string{"scheduled", "claimed", "renewed", "rescheduled"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}
