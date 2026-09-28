package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
)

const testOperationalHistoryRetention = 30 * 24 * time.Hour

type retentionParticipationFacts struct {
	firstParticipatedAt string
	initialVersion      string
	initialIdentity     string
	latestCheckAt       string
	latestOutcome       string
}

func TestPurgeOperationalHistoryPreservesSemanticState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	verificationStore := New(pool)

	maintenance, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	now := time.Now().UTC()

	participating := mustStoreOrigin(
		t,
		"https://participating.example",
	)
	withdrawn := mustStoreOrigin(
		t,
		"https://withdrawn.example",
	)
	unknown := mustStoreOrigin(
		t,
		"https://unknown.example",
	)
	recent := mustStoreOrigin(
		t,
		"https://recent.example",
	)

	record := func(
		source string,
		observedAt time.Time,
		outcome declaration.Outcome,
	) {
		t.Helper()

		parsed := mustStoreOrigin(
			t,
			source,
		)

		result := declaration.Result{
			Outcome: outcome,
			Origin:  parsed,
		}

		if outcome == declaration.OutcomeValid {
			result.Declaration =
				declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				}
		}

		if err := verificationStore.RecordVerification(
			ctx,
			observedAt,
			result,
		); err != nil {
			t.Fatalf(
				"record %q observation for %q: %v",
				outcomeToText[outcome],
				source,
				err,
			)
		}
	}

	record(
		participating.String(),
		now.Add(-100*24*time.Hour),
		declaration.OutcomeAbsent,
	)
	record(
		participating.String(),
		now.Add(-90*24*time.Hour),
		declaration.OutcomeValid,
	)
	record(
		participating.String(),
		now.Add(-80*24*time.Hour),
		declaration.OutcomeUnavailable,
	)

	record(
		withdrawn.String(),
		now.Add(-100*24*time.Hour),
		declaration.OutcomeValid,
	)
	record(
		withdrawn.String(),
		now.Add(-90*24*time.Hour),
		declaration.OutcomeAbsent,
	)
	record(
		withdrawn.String(),
		now.Add(-80*24*time.Hour),
		declaration.OutcomeUnavailable,
	)

	record(
		unknown.String(),
		now.Add(-70*24*time.Hour),
		declaration.OutcomeRobotsDenied,
	)

	record(
		recent.String(),
		now.Add(-60*24*time.Hour),
		declaration.OutcomeInvalid,
	)
	record(
		recent.String(),
		now.Add(-24*time.Hour),
		declaration.OutcomeInvalid,
	)

	for _, source := range []string{
		participating.String(),
		withdrawn.String(),
	} {
		if _, err := pool.Exec(
			ctx,
			`
				INSERT INTO discovery_source_state (
					source_origin,
					seeded
				)
				VALUES ($1, TRUE)
			`,
			source,
		); err != nil {
			t.Fatalf(
				"insert discovery source %q: %v",
				source,
				err,
			)
		}
	}

	const candidateOrigin = "https://candidate.example"
	const sourceOrigin = "https://directory.example"

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			VALUES ($1, $2, $2)
		`,
		candidateOrigin,
		now.Add(-180*24*time.Hour),
	); err != nil {
		t.Fatalf(
			"insert discovery candidate: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_edges (
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			)
			VALUES ($1, $2, 'link', $3, $3)
		`,
		sourceOrigin,
		candidateOrigin,
		now.Add(-180*24*time.Hour),
	); err != nil {
		t.Fatalf(
			"insert discovery edge: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO operator_audit_events (
				occurred_at,
				action,
				target,
				caller,
				actor,
				result,
				reason
			)
			VALUES (
				$1,
				'retention.test',
				'history',
				'127.0.0.1',
				'test',
				'success',
				'retained independently'
			)
		`,
		now.Add(-180*24*time.Hour),
	); err != nil {
		t.Fatalf(
			"insert operator audit: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue_events (
				origin,
				occurred_at,
				event,
				mode
			)
			VALUES
				(
					'https://old-event.example',
					$1,
					'scheduled',
					'probe'
				),
				(
					'https://recent-event.example',
					$2,
					'scheduled',
					'probe'
				)
		`,
		now.Add(-60*24*time.Hour),
		now.Add(-24*time.Hour),
	); err != nil {
		t.Fatalf(
			"insert verification queue events: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO crawl_service_heartbeats (
				service,
				instance_id,
				state,
				current_origin,
				message,
				started_at,
				updated_at
			)
			VALUES
				(
					'worker',
					'stale-worker',
					'stopping',
					'',
					'',
					$1,
					$1
				),
				(
					'worker',
					'recent-worker',
					'idle',
					'',
					'',
					$2,
					$2
				)
		`,
		now.Add(-60*24*time.Hour),
		now.Add(-24*time.Hour),
	); err != nil {
		t.Fatalf(
			"insert service heartbeats: %v",
			err,
		)
	}

	beforeWithdrawn :=
		readRetentionParticipationFacts(
			t,
			pool,
			withdrawn.String(),
		)

	if err := maintenance.PurgeOperationalHistory(
		ctx,
		testOperationalHistoryRetention,
	); err != nil {
		t.Fatalf(
			"PurgeOperationalHistory() error = %v",
			err,
		)
	}

	assertRetentionObservationCount(
		t,
		pool,
		participating.String(),
		2,
	)
	assertRetentionObservationCount(
		t,
		pool,
		withdrawn.String(),
		2,
	)
	assertRetentionObservationCount(
		t,
		pool,
		unknown.String(),
		1,
	)
	assertRetentionObservationCount(
		t,
		pool,
		recent.String(),
		1,
	)

	afterWithdrawn :=
		readRetentionParticipationFacts(
			t,
			pool,
			withdrawn.String(),
		)

	if afterWithdrawn != beforeWithdrawn {
		t.Errorf(
			"withdrawn participation facts after purge = %#v, want %#v",
			afterWithdrawn,
			beforeWithdrawn,
		)
	}

	verified, err :=
		verificationStore.VerifiedOrigins(
			ctx,
		)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v",
			err,
		)
	}

	if len(verified) != 1 {
		t.Fatalf(
			"verified origins = %#v, want one participating origin",
			verified,
		)
	}

	if verified[0].Origin != participating {
		t.Errorf(
			"verified origin = %v, want %v",
			verified[0].Origin,
			participating,
		)
	}

	if verified[0].Declaration !=
		(declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		}) {
		t.Errorf(
			"verified declaration = %#v",
			verified[0].Declaration,
		)
	}

	sources, err := maintenance.CrawlSources(
		ctx,
	)
	if err != nil {
		t.Fatalf(
			"CrawlSources() error = %v",
			err,
		)
	}

	verifiedByOrigin := make(
		map[string]bool,
		len(sources),
	)

	for _, source := range sources {
		verifiedByOrigin[source.Origin.String()] = source.Verified
	}

	if !verifiedByOrigin[participating.String()] {
		t.Error(
			"participating crawl source lost verified state",
		)
	}

	if verifiedByOrigin[withdrawn.String()] {
		t.Error(
			"withdrawn crawl source became verified",
		)
	}

	var queueEventCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue_events
		`,
	).Scan(&queueEventCount); err != nil {
		t.Fatalf(
			"count queue events: %v",
			err,
		)
	}

	if queueEventCount != 1 {
		t.Errorf(
			"queue event count = %d, want 1",
			queueEventCount,
		)
	}

	var heartbeatCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM crawl_service_heartbeats
			WHERE instance_id IN (
				'stale-worker',
				'recent-worker'
			)
		`,
	).Scan(&heartbeatCount); err != nil {
		t.Fatalf(
			"count service heartbeats: %v",
			err,
		)
	}

	if heartbeatCount != 1 {
		t.Errorf(
			"service heartbeat count = %d, want 1",
			heartbeatCount,
		)
	}

	var recentHeartbeatCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM crawl_service_heartbeats
			WHERE instance_id = 'recent-worker'
		`,
	).Scan(&recentHeartbeatCount); err != nil {
		t.Fatalf(
			"count recent heartbeat: %v",
			err,
		)
	}

	if recentHeartbeatCount != 1 {
		t.Errorf(
			"recent heartbeat count = %d, want 1",
			recentHeartbeatCount,
		)
	}

	var candidateCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_candidates
			WHERE origin = $1
		`,
		candidateOrigin,
	).Scan(&candidateCount); err != nil {
		t.Fatalf(
			"count discovery candidates: %v",
			err,
		)
	}

	if candidateCount != 1 {
		t.Errorf(
			"discovery candidate count = %d, want 1",
			candidateCount,
		)
	}

	var edgeCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_edges
			WHERE source_origin = $1
				AND candidate_origin = $2
		`,
		sourceOrigin,
		candidateOrigin,
	).Scan(&edgeCount); err != nil {
		t.Fatalf(
			"count discovery edges: %v",
			err,
		)
	}

	if edgeCount != 1 {
		t.Errorf(
			"discovery edge count = %d, want 1",
			edgeCount,
		)
	}

	var auditCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM operator_audit_events
			WHERE action = 'retention.test'
		`,
	).Scan(&auditCount); err != nil {
		t.Fatalf(
			"count operator audits: %v",
			err,
		)
	}

	if auditCount != 1 {
		t.Errorf(
			"operator audit count = %d, want 1",
			auditCount,
		)
	}

	var withdrawnMissCount int64

	if err := pool.QueryRow(
		ctx,
		`
			SELECT miss_count
			FROM verification_reprobe_state
			WHERE origin = $1
		`,
		withdrawn.String(),
	).Scan(&withdrawnMissCount); err != nil {
		t.Fatalf(
			"read withdrawn reprobe state: %v",
			err,
		)
	}

	if withdrawnMissCount != 1 {
		t.Errorf(
			"withdrawn reprobe miss count = %d, want 1",
			withdrawnMissCount,
		)
	}
}

func TestPurgeOperationalHistoryRejectsInvalidInputs(
	t *testing.T,
) {
	ctx := context.Background()

	var missing *DiscoveryStore

	if err := missing.PurgeOperationalHistory(
		ctx,
		testOperationalHistoryRetention,
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Errorf(
			"nil store error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	pool := newStoreTestPool(t)

	storage, err := NewDiscoveryStore(
		pool,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	if err := storage.PurgeOperationalHistory(
		ctx,
		0,
	); !errors.Is(
		err,
		errInvalidOperationalHistoryRetention,
	) {
		t.Errorf(
			"zero retention error = %v, want %v",
			err,
			errInvalidOperationalHistoryRetention,
		)
	}

	canceled, cancel :=
		context.WithCancel(
			ctx,
		)
	cancel()

	if err := storage.PurgeOperationalHistory(
		canceled,
		testOperationalHistoryRetention,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"canceled context error = %v, want context.Canceled",
			err,
		)
	}
}

func TestPurgeOperationalHistoryReturnsObservationDeleteFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	storage, err := NewDiscoveryStore(
		pool,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			ALTER TABLE verification_observations
			RENAME TO verification_observations_missing
		`,
	); err != nil {
		t.Fatalf(
			"rename verification observations: %v",
			err,
		)
	}

	if err := storage.PurgeOperationalHistory(
		ctx,
		testOperationalHistoryRetention,
	); err == nil {
		t.Error(
			"PurgeOperationalHistory() error = nil, want non-nil",
		)
	}
}

func TestPurgeOperationalHistoryRollsBackWhenQueueEventDeleteFails(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	verificationStore := New(pool)

	storage, err := NewDiscoveryStore(
		pool,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	source := mustStoreOrigin(
		t,
		"https://queue-rollback.example",
	)

	now := time.Now().UTC()

	for _, observedAt := range []time.Time{
		now.Add(-60 * 24 * time.Hour),
		now.Add(-50 * 24 * time.Hour),
	} {
		if err := verificationStore.RecordVerification(
			ctx,
			observedAt,
			declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
			},
		); err != nil {
			t.Fatalf(
				"RecordVerification() error = %v",
				err,
			)
		}
	}

	if _, err := pool.Exec(
		ctx,
		`
			ALTER TABLE verification_queue_events
			RENAME TO verification_queue_events_missing
		`,
	); err != nil {
		t.Fatalf(
			"rename verification queue events: %v",
			err,
		)
	}

	if err := storage.PurgeOperationalHistory(
		ctx,
		testOperationalHistoryRetention,
	); err == nil {
		t.Fatal(
			"PurgeOperationalHistory() error = nil, want non-nil",
		)
	}

	assertRetentionObservationCount(
		t,
		pool,
		source.String(),
		2,
	)
}

func TestPurgeOperationalHistoryRollsBackWhenHeartbeatDeleteFails(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	verificationStore := New(pool)

	storage, err := NewDiscoveryStore(
		pool,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	source := mustStoreOrigin(
		t,
		"https://heartbeat-rollback.example",
	)

	now := time.Now().UTC()

	for _, observedAt := range []time.Time{
		now.Add(-60 * 24 * time.Hour),
		now.Add(-50 * 24 * time.Hour),
	} {
		if err := verificationStore.RecordVerification(
			ctx,
			observedAt,
			declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
			},
		); err != nil {
			t.Fatalf(
				"RecordVerification() error = %v",
				err,
			)
		}
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue_events (
				origin,
				occurred_at,
				event,
				mode
			)
			VALUES (
				'https://rollback-event.example',
				$1,
				'scheduled',
				'probe'
			)
		`,
		now.Add(-60*24*time.Hour),
	); err != nil {
		t.Fatalf(
			"insert queue event: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			ALTER TABLE crawl_service_heartbeats
			RENAME TO crawl_service_heartbeats_missing
		`,
	); err != nil {
		t.Fatalf(
			"rename service heartbeats: %v",
			err,
		)
	}

	if err := storage.PurgeOperationalHistory(
		ctx,
		testOperationalHistoryRetention,
	); err == nil {
		t.Fatal(
			"PurgeOperationalHistory() error = nil, want non-nil",
		)
	}

	assertRetentionObservationCount(
		t,
		pool,
		source.String(),
		2,
	)

	var queueEventCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue_events
			WHERE origin =
				'https://rollback-event.example'
		`,
	).Scan(&queueEventCount); err != nil {
		t.Fatalf(
			"count rollback queue event: %v",
			err,
		)
	}

	if queueEventCount != 1 {
		t.Errorf(
			"rollback queue event count = %d, want 1",
			queueEventCount,
		)
	}
}

func readRetentionParticipationFacts(
	t *testing.T,
	pool *pgxpool.Pool,
	rawOrigin string,
) retentionParticipationFacts {
	t.Helper()

	var facts retentionParticipationFacts

	if err := pool.QueryRow(
		context.Background(),
		`
			SELECT
				COALESCE(
					first_participated_at::text,
					''
				),
				COALESCE(
					initial_declaration_version::text,
					''
				),
				COALESCE(
					initial_declaration_identity,
					''
				),
				COALESCE(
					latest_declaration_check_at::text,
					''
				),
				COALESCE(
					latest_declaration_check_outcome,
					''
				)
			FROM origins
			WHERE origin = $1
		`,
		rawOrigin,
	).Scan(
		&facts.firstParticipatedAt,
		&facts.initialVersion,
		&facts.initialIdentity,
		&facts.latestCheckAt,
		&facts.latestOutcome,
	); err != nil {
		t.Fatalf(
			"read participation facts for %q: %v",
			rawOrigin,
			err,
		)
	}

	return facts
}

func assertRetentionObservationCount(
	t *testing.T,
	pool *pgxpool.Pool,
	rawOrigin string,
	want int,
) {
	t.Helper()

	var got int

	if err := pool.QueryRow(
		context.Background(),
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		rawOrigin,
	).Scan(&got); err != nil {
		t.Fatalf(
			"count observations for %q: %v",
			rawOrigin,
			err,
		)
	}

	if got != want {
		t.Errorf(
			"observation count for %q = %d, want %d",
			rawOrigin,
			got,
			want,
		)
	}
}
