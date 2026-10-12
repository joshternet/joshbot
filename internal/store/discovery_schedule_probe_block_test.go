package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
)

func TestScheduleProbeCandidatesSkipsCrawlBlockedOrigins(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	source := mustStoreOrigin(t, "https://source.example")
	blocked := mustStoreOrigin(t, "https://blocked.example")
	open := mustStoreOrigin(t, "https://open.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCrawlBlocked(ctx, blocked, true); err != nil {
		t.Fatal(err)
	}

	if err := store.scheduleProbeCandidates(
		ctx,
		[]discovery.Candidate{
			{Origin: blocked, Kind: discovery.KindLink},
			{Origin: open, Kind: discovery.KindLink},
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatalf(
			"scheduleProbeCandidates() error = %v",
			err,
		)
	}

	var blockedQueued, openQueued bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM verification_queue
				WHERE origin = $1
			),
			EXISTS (
				SELECT 1
				FROM verification_queue
				WHERE origin = $2
			)
	`, blocked.String(), open.String()).Scan(
		&blockedQueued,
		&openQueued,
	); err != nil {
		t.Fatal(err)
	}

	if blockedQueued {
		t.Fatal("crawl-blocked origin was probe-scheduled")
	}
	if !openQueued {
		t.Fatal("open origin was not probe-scheduled")
	}
}
