package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

var errDiscoveryTestClock = errors.New(
	"test discovery clock failure",
)

func TestDiscoveryStoreClaimsEveryValidIdentity(
	t *testing.T,
) {
	tests := []struct {
		name     string
		identity declaration.Identity
	}{
		{
			name:     "undeclared",
			identity: declaration.IdentityUndeclared,
		},
		{
			name:     "affirmed",
			identity: declaration.IdentityAffirmed,
		},
		{
			name:     "declined",
			identity: declaration.IdentityDeclined,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)
			now := queueTestTime()
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)

			seedDiscoveryTestObservation(
				t,
				pool,
				source,
				now.Add(-time.Hour),
				declaration.OutcomeValid,
				test.identity,
			)

			discoveryStore := newDiscoveryTestStore(
				t,
				pool,
				now,
			)

			got, found, err :=
				discoveryStore.ClaimDiscoverySource(
					ctx,
					24*time.Hour,
				)
			if err != nil {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v, want nil",
					err,
				)
			}

			if !found {
				t.Fatal(
					"ClaimDiscoverySource() found = false, want true",
				)
			}

			if got != source {
				t.Errorf(
					"ClaimDiscoverySource() source = %q, want %q",
					got,
					source,
				)
			}

			var attemptedAt time.Time
			err = pool.QueryRow(
				ctx,
				`
					SELECT last_attempted_at
					FROM discovery_source_state
					WHERE source_origin = $1
				`,
				source.String(),
			).Scan(&attemptedAt)
			if err != nil {
				t.Fatalf(
					"query discovery source state: %v",
					err,
				)
			}

			if !attemptedAt.Equal(now) {
				t.Errorf(
					"last attempted at = %v, want %v",
					attemptedAt,
					now,
				)
			}
		})
	}
}

func TestDiscoveryStoreExcludesNonVerifiedSources(
	t *testing.T,
) {
	outcomes := []declaration.Outcome{
		declaration.OutcomeAbsent,
		declaration.OutcomeInvalid,
		declaration.OutcomeUnsupportedVersion,
		declaration.OutcomeCrossOriginRedirect,
	}

	for _, outcome := range outcomes {
		t.Run(
			discoveryTestOutcomeName(outcome),
			func(t *testing.T) {
				ctx := context.Background()
				pool := newStoreTestPool(t)
				now := queueTestTime()
				source := mustStoreOrigin(
					t,
					"https://example.com",
				)

				seedDiscoveryTestObservation(
					t,
					pool,
					source,
					now.Add(-time.Hour),
					outcome,
					0,
				)

				discoveryStore :=
					newDiscoveryTestStore(
						t,
						pool,
						now,
					)

				got, found, err :=
					discoveryStore.
						ClaimDiscoverySource(
							ctx,
							time.Hour,
						)
				if err != nil {
					t.Fatalf(
						"ClaimDiscoverySource() error = %v, want nil",
						err,
					)
				}

				if found {
					t.Errorf(
						"ClaimDiscoverySource() = %q, true, want zero, false",
						got,
					)
				}
			},
		)
	}

	t.Run("no authoritative observation", func(t *testing.T) {
		ctx := context.Background()
		pool := newStoreTestPool(t)
		now := queueTestTime()

		_, err := pool.Exec(
			ctx,
			`
				INSERT INTO origins (
					origin,
					first_observed_at
				)
				VALUES (
					'https://example.com',
					$1
				)
			`,
			now,
		)
		if err != nil {
			t.Fatalf("insert origin: %v", err)
		}

		discoveryStore := newDiscoveryTestStore(
			t,
			pool,
			now,
		)

		got, found, err :=
			discoveryStore.ClaimDiscoverySource(
				ctx,
				time.Hour,
			)
		if err != nil {
			t.Fatalf(
				"ClaimDiscoverySource() error = %v, want nil",
				err,
			)
		}

		if found {
			t.Errorf(
				"ClaimDiscoverySource() = %q, true, want zero, false",
				got,
			)
		}
	})
}

func TestDiscoveryStoreTemporaryFailuresPreserveEligibility(
	t *testing.T,
) {
	outcomes := []declaration.Outcome{
		declaration.OutcomeUnavailable,
		declaration.OutcomeRobotsDenied,
	}

	for _, outcome := range outcomes {
		t.Run(
			discoveryTestOutcomeName(outcome),
			func(t *testing.T) {
				ctx := context.Background()
				pool := newStoreTestPool(t)
				now := queueTestTime()
				source := mustStoreOrigin(
					t,
					"https://example.com",
				)

				seedDiscoveryTestObservation(
					t,
					pool,
					source,
					now.Add(-2*time.Hour),
					declaration.OutcomeValid,
					declaration.IdentityUndeclared,
				)
				seedDiscoveryTestObservation(
					t,
					pool,
					source,
					now.Add(-time.Hour),
					outcome,
					0,
				)

				discoveryStore :=
					newDiscoveryTestStore(
						t,
						pool,
						now,
					)

				got, found, err :=
					discoveryStore.
						ClaimDiscoverySource(
							ctx,
							time.Hour,
						)
				if err != nil {
					t.Fatalf(
						"ClaimDiscoverySource() error = %v, want nil",
						err,
					)
				}

				if !found || got != source {
					t.Errorf(
						"ClaimDiscoverySource() = %q, %v, want %q, true",
						got,
						found,
						source,
					)
				}
			},
		)
	}
}

func TestDiscoveryStoreUsesDeterministicClaimOrderAndInterval(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	start := queueTestTime()
	first := mustStoreOrigin(t, "https://a.example")
	second := mustStoreOrigin(t, "https://b.example")

	for _, source := range []origin.Origin{
		second,
		first,
	} {
		seedDiscoveryTestObservation(
			t,
			pool,
			source,
			start,
			declaration.OutcomeValid,
			declaration.IdentityUndeclared,
		)
	}

	interval := time.Hour
	clock := &discoveryTestClock{
		times: []time.Time{
			start.Add(2 * time.Hour),
			start.Add(2 * time.Hour),
			start.Add(3*time.Hour - time.Nanosecond),
			start.Add(3 * time.Hour),
		},
	}
	discoveryStore, err := newDiscoveryStore(
		pool,
		clock,
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	got, found, err :=
		discoveryStore.ClaimDiscoverySource(
			ctx,
			interval,
		)
	if err != nil {
		t.Fatalf("first claim error = %v", err)
	}
	if !found || got != first {
		t.Errorf(
			"first claim = %q, %v, want %q, true",
			got,
			found,
			first,
		)
	}

	got, found, err =
		discoveryStore.ClaimDiscoverySource(
			ctx,
			interval,
		)
	if err != nil {
		t.Fatalf("second claim error = %v", err)
	}
	if !found || got != second {
		t.Errorf(
			"second claim = %q, %v, want %q, true",
			got,
			found,
			second,
		)
	}

	got, found, err =
		discoveryStore.ClaimDiscoverySource(
			ctx,
			interval,
		)
	if err != nil {
		t.Fatalf("early claim error = %v", err)
	}
	if found || got != (origin.Origin{}) {
		t.Errorf(
			"early claim = %q, %v, want zero, false",
			got,
			found,
		)
	}

	got, found, err =
		discoveryStore.ClaimDiscoverySource(
			ctx,
			interval,
		)
	if err != nil {
		t.Fatalf("due claim error = %v", err)
	}
	if !found || got != first {
		t.Errorf(
			"due claim = %q, %v, want %q, true",
			got,
			found,
			first,
		)
	}
}

func TestDiscoveryStoreUsesPostgreSQLClock(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		queueTestTime(),
		declaration.OutcomeValid,
		declaration.IdentityAffirmed,
	)

	discoveryStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	before := time.Now().UTC()
	got, found, err :=
		discoveryStore.ClaimDiscoverySource(
			ctx,
			time.Hour,
		)
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySource() error = %v, want nil",
			err,
		)
	}

	if !found || got != source {
		t.Errorf(
			"claim = %q, %v, want %q, true",
			got,
			found,
			source,
		)
	}

	var attemptedAt time.Time
	err = pool.QueryRow(
		ctx,
		`
			SELECT last_attempted_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(&attemptedAt)
	if err != nil {
		t.Fatalf(
			"query discovery source state: %v",
			err,
		)
	}

	if attemptedAt.Before(before) ||
		attemptedAt.After(after) {
		t.Errorf(
			"last attempted at = %v, want between %v and %v",
			attemptedAt,
			before,
			after,
		)
	}
}

func TestDiscoveryStoreConcurrentClaimsReturnOneSource(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		now.Add(-time.Hour),
		declaration.OutcomeValid,
		declaration.IdentityAffirmed,
	)

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	const claimantCount = 8

	start := make(chan struct{})
	results := make(
		chan discoveryTestClaim,
		claimantCount,
	)

	var group sync.WaitGroup
	for range claimantCount {
		group.Add(1)

		go func() {
			defer group.Done()
			<-start

			got, found, err :=
				discoveryStore.
					ClaimDiscoverySource(
						ctx,
						time.Hour,
					)
			results <- discoveryTestClaim{
				source: got,
				found:  found,
				err:    err,
			}
		}()
	}

	close(start)
	group.Wait()
	close(results)

	foundCount := 0
	for result := range results {
		if result.err != nil {
			t.Errorf(
				"ClaimDiscoverySource() error = %v",
				result.err,
			)
			continue
		}

		if result.found {
			foundCount++

			if result.source != source {
				t.Errorf(
					"claimed source = %q, want %q",
					result.source,
					source,
				)
			}
		}
	}

	if foundCount != 1 {
		t.Errorf(
			"successful claim count = %d, want 1",
			foundCount,
		)
	}
}

func TestDiscoveryStoreSkipsLockedSource(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	first := mustStoreOrigin(t, "https://a.example")
	second := mustStoreOrigin(t, "https://b.example")

	for _, source := range []origin.Origin{
		first,
		second,
	} {
		seedDiscoveryTestObservation(
			t,
			pool,
			source,
			now.Add(-time.Hour),
			declaration.OutcomeValid,
			declaration.IdentityUndeclared,
		)
	}

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	var locked string
	err = transaction.QueryRow(
		ctx,
		`
			SELECT origin
			FROM origins
			WHERE origin = $1
			FOR UPDATE
		`,
		first.String(),
	).Scan(&locked)
	if err != nil {
		t.Fatalf("lock first source: %v", err)
	}

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	got, found, err :=
		discoveryStore.ClaimDiscoverySource(
			ctx,
			time.Hour,
		)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySource() error = %v, want nil",
			err,
		)
	}

	if !found || got != second {
		t.Errorf(
			"claim = %q, %v, want %q, true",
			got,
			found,
			second,
		)
	}
}

func TestRecordDiscoveryCreatesPrivateProvenanceAndProbeWork(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	link := mustStoreOrigin(
		t,
		"https://link.example",
	)
	redirect := mustStoreOrigin(
		t,
		"https://redirect.example",
	)

	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		now.Add(-time.Hour),
		declaration.OutcomeValid,
		declaration.IdentityAffirmed,
	)

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	result, err := discoveryStore.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{
			{
				Origin: redirect,
				Kind:   discovery.KindRedirect,
			},
			{
				Origin: link,
				Kind:   discovery.KindLink,
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v, want nil",
			err,
		)
	}

	wantResult := discovery.RecordResult{
		Accepted: 2,
	}
	if result != wantResult {
		t.Errorf(
			"RecordDiscovery() result = %#v, want %#v",
			result,
			wantResult,
		)
	}

	var candidateCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_candidates
		`,
	).Scan(&candidateCount)
	if err != nil {
		t.Fatalf("count candidates: %v", err)
	}

	if candidateCount != 2 {
		t.Errorf(
			"candidate count = %d, want 2",
			candidateCount,
		)
	}

	rows, err := pool.Query(
		ctx,
		`
			SELECT
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			FROM discovery_edges
			ORDER BY candidate_origin
		`,
	)
	if err != nil {
		t.Fatalf("query discovery edges: %v", err)
	}
	defer rows.Close()

	gotEdges := make([]string, 0, 2)

	for rows.Next() {
		var (
			storedSource    string
			storedCandidate string
			kind            string
			firstAt         time.Time
			lastAt          time.Time
		)

		err := rows.Scan(
			&storedSource,
			&storedCandidate,
			&kind,
			&firstAt,
			&lastAt,
		)
		if err != nil {
			t.Fatalf("scan discovery edge: %v", err)
		}

		if storedSource != source.String() {
			t.Errorf(
				"edge source = %q, want %q",
				storedSource,
				source,
			)
		}

		if !firstAt.Equal(now) ||
			!lastAt.Equal(now) {
			t.Errorf(
				"edge times = %v, %v, want %v",
				firstAt,
				lastAt,
				now,
			)
		}

		gotEdges = append(
			gotEdges,
			storedCandidate+"|"+kind,
		)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate discovery edges: %v", err)
	}

	wantEdges := []string{
		"https://link.example|link",
		"https://redirect.example|redirect",
	}
	if !reflect.DeepEqual(gotEdges, wantEdges) {
		t.Errorf(
			"edges = %#v, want %#v",
			gotEdges,
			wantEdges,
		)
	}

	queueRows, err := pool.Query(
		ctx,
		`
			SELECT
				origin,
				mode,
				available_at
			FROM verification_queue
			ORDER BY origin
		`,
	)
	if err != nil {
		t.Fatalf("query verification queue: %v", err)
	}
	defer queueRows.Close()

	gotQueue := make([]string, 0, 2)

	for queueRows.Next() {
		var (
			storedOrigin string
			mode         string
			availableAt  time.Time
		)

		err := queueRows.Scan(
			&storedOrigin,
			&mode,
			&availableAt,
		)
		if err != nil {
			t.Fatalf("scan verification queue: %v", err)
		}

		if !availableAt.Equal(now) {
			t.Errorf(
				"available at = %v, want %v",
				availableAt,
				now,
			)
		}

		gotQueue = append(
			gotQueue,
			storedOrigin+"|"+mode,
		)
	}

	if err := queueRows.Err(); err != nil {
		t.Fatalf("iterate verification queue: %v", err)
	}

	wantQueue := []string{
		"https://link.example|probe",
		"https://redirect.example|probe",
	}
	if !reflect.DeepEqual(gotQueue, wantQueue) {
		t.Errorf(
			"queue = %#v, want %#v",
			gotQueue,
			wantQueue,
		)
	}

	var observationOriginCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM origins
			WHERE origin = ANY($1::text[])
		`,
		[]string{
			link.String(),
			redirect.String(),
		},
	).Scan(&observationOriginCount)
	if err != nil {
		t.Fatalf(
			"count candidate observation origins: %v",
			err,
		)
	}

	if observationOriginCount != 0 {
		t.Errorf(
			"candidate observation origin count = %d, want 0",
			observationOriginCount,
		)
	}
}

func TestRecordDiscoveryPreservesFirstAndUpdatesLast(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	firstAt := queueTestTime()
	lastAt := firstAt.Add(24 * time.Hour)
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	candidate := mustStoreOrigin(
		t,
		"https://example.net",
	)

	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		firstAt.Add(-time.Hour),
		declaration.OutcomeValid,
		declaration.IdentityUndeclared,
	)

	clock := &discoveryTestClock{
		times: []time.Time{
			firstAt,
			lastAt,
		},
	}
	discoveryStore, err := newDiscoveryStore(
		pool,
		clock,
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	candidates := []discovery.Candidate{
		{
			Origin: candidate,
			Kind:   discovery.KindLink,
		},
	}

	for attempt := range 2 {
		result, err := discoveryStore.RecordDiscovery(
			ctx,
			source,
			candidates,
		)
		if err != nil {
			t.Fatalf(
				"RecordDiscovery() attempt %d error = %v",
				attempt,
				err,
			)
		}

		if result.Accepted != 1 ||
			result.Dropped != 0 {
			t.Errorf(
				"attempt %d result = %#v",
				attempt,
				result,
			)
		}
	}

	var (
		candidateFirst time.Time
		candidateLast  time.Time
		edgeFirst      time.Time
		edgeLast       time.Time
	)

	err = pool.QueryRow(
		ctx,
		`
			SELECT
				candidate.first_discovered_at,
				candidate.last_discovered_at,
				edge.first_discovered_at,
				edge.last_discovered_at
			FROM discovery_candidates AS candidate
			JOIN discovery_edges AS edge
				ON edge.candidate_origin =
					candidate.origin
			WHERE candidate.origin = $1
		`,
		candidate.String(),
	).Scan(
		&candidateFirst,
		&candidateLast,
		&edgeFirst,
		&edgeLast,
	)
	if err != nil {
		t.Fatalf("query discovery times: %v", err)
	}

	if !candidateFirst.Equal(firstAt) {
		t.Errorf(
			"candidate first = %v, want %v",
			candidateFirst,
			firstAt,
		)
	}

	if !candidateLast.Equal(lastAt) {
		t.Errorf(
			"candidate last = %v, want %v",
			candidateLast,
			lastAt,
		)
	}

	if !edgeFirst.Equal(firstAt) {
		t.Errorf(
			"edge first = %v, want %v",
			edgeFirst,
			firstAt,
		)
	}

	if !edgeLast.Equal(lastAt) {
		t.Errorf(
			"edge last = %v, want %v",
			edgeLast,
			lastAt,
		)
	}

	var queueCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue
			WHERE origin = $1
		`,
		candidate.String(),
	).Scan(&queueCount)
	if err != nil {
		t.Fatalf("count probe queue rows: %v", err)
	}

	if queueCount != 1 {
		t.Errorf(
			"probe queue count = %d, want 1",
			queueCount,
		)
	}
}

func TestRecordDiscoveryLeavesExistingQueueUntouched(
	t *testing.T,
) {
	for _, mode := range []string{
		"probe",
		"recurring",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)
			now := queueTestTime()
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)
			candidate := mustStoreOrigin(
				t,
				"https://example.net",
			)
			availableAt := now.Add(-time.Hour)
			claimedAt := now.Add(-time.Minute)
			expiresAt := now.Add(time.Hour)

			seedDiscoveryTestObservation(
				t,
				pool,
				source,
				now.Add(-2*time.Hour),
				declaration.OutcomeValid,
				declaration.IdentityAffirmed,
			)

			_, err := pool.Exec(
				ctx,
				`
					INSERT INTO verification_queue (
						origin,
						available_at,
						mode,
						lease_generation,
						lease_owner,
						lease_expires_at,
						last_claimed_at
					)
					VALUES (
						$1,
						$2,
						$3,
						7,
						'worker-a',
						$4,
						$5
					)
				`,
				candidate.String(),
				availableAt,
				mode,
				expiresAt,
				claimedAt,
			)
			if err != nil {
				t.Fatalf(
					"insert existing queue row: %v",
					err,
				)
			}

			before := readDiscoveryTestQueueState(
				t,
				ctx,
				pool,
				candidate,
			)

			discoveryStore :=
				newDiscoveryTestStore(
					t,
					pool,
					now,
				)

			result, err :=
				discoveryStore.RecordDiscovery(
					ctx,
					source,
					[]discovery.Candidate{
						{
							Origin: candidate,
							Kind:   discovery.KindLink,
						},
					},
				)
			if err != nil {
				t.Fatalf(
					"RecordDiscovery() error = %v, want nil",
					err,
				)
			}

			if result.Accepted != 1 ||
				result.Dropped != 0 {
				t.Errorf(
					"RecordDiscovery() result = %#v",
					result,
				)
			}

			after := readDiscoveryTestQueueState(
				t,
				ctx,
				pool,
				candidate,
			)

			if !reflect.DeepEqual(after, before) {
				t.Errorf(
					"queue changed from %#v to %#v",
					before,
					after,
				)
			}
		})
	}
}

func TestRecordDiscoveryEnforcesHistoricalCap(
	t *testing.T,
) {
	t.Run("existing relationship updates at cap", func(t *testing.T) {
		ctx := context.Background()
		pool := newStoreTestPool(t)
		now := queueTestTime()
		source := seedDiscoveryTestSource(
			t,
			pool,
			"https://example.com",
			now,
		)
		existing := seedDiscoveryTestHistory(
			t,
			pool,
			source,
			maxDiscoveryCandidatesPerSource,
			now.Add(-time.Hour),
		)
		newCandidate := mustStoreOrigin(
			t,
			"https://new.example",
		)

		discoveryStore := newDiscoveryTestStore(
			t,
			pool,
			now,
		)

		result, err := discoveryStore.RecordDiscovery(
			ctx,
			source,
			[]discovery.Candidate{
				{
					Origin: newCandidate,
					Kind:   discovery.KindLink,
				},
				{
					Origin: mustStoreOrigin(
						t,
						existing[0],
					),
					Kind: discovery.KindLink,
				},
			},
		)
		if err != nil {
			t.Fatalf(
				"RecordDiscovery() error = %v, want nil",
				err,
			)
		}

		want := discovery.RecordResult{
			Accepted: 1,
			Dropped:  1,
		}
		if result != want {
			t.Errorf(
				"RecordDiscovery() result = %#v, want %#v",
				result,
				want,
			)
		}

		var newCount int
		err = pool.QueryRow(
			ctx,
			`
				SELECT count(*)
				FROM discovery_candidates
				WHERE origin = $1
			`,
			newCandidate.String(),
		).Scan(&newCount)
		if err != nil {
			t.Fatalf("count new candidate: %v", err)
		}

		if newCount != 0 {
			t.Errorf(
				"new candidate count = %d, want 0",
				newCount,
			)
		}

		var updatedAt time.Time
		err = pool.QueryRow(
			ctx,
			`
				SELECT last_discovered_at
				FROM discovery_edges
				WHERE source_origin = $1
					AND candidate_origin = $2
			`,
			source.String(),
			existing[0],
		).Scan(&updatedAt)
		if err != nil {
			t.Fatalf(
				"query existing edge: %v",
				err,
			)
		}

		if !updatedAt.Equal(now) {
			t.Errorf(
				"existing edge last discovered = %v, want %v",
				updatedAt,
				now,
			)
		}
	})

	t.Run("near cap admits canonical first candidates", func(t *testing.T) {
		ctx := context.Background()
		pool := newStoreTestPool(t)
		now := queueTestTime()
		source := seedDiscoveryTestSource(
			t,
			pool,
			"https://example.com",
			now,
		)

		seedDiscoveryTestHistory(
			t,
			pool,
			source,
			maxDiscoveryCandidatesPerSource-2,
			now.Add(-time.Hour),
		)

		inputOrigins := []string{
			"https://z-new.example",
			"https://b-new.example",
			"https://y-new.example",
			"https://a-new.example",
		}
		input := make(
			[]discovery.Candidate,
			0,
			len(inputOrigins),
		)
		for _, rawOrigin := range inputOrigins {
			input = append(
				input,
				discovery.Candidate{
					Origin: mustStoreOrigin(
						t,
						rawOrigin,
					),
					Kind: discovery.KindLink,
				},
			)
		}

		discoveryStore := newDiscoveryTestStore(
			t,
			pool,
			now,
		)
		result, err := discoveryStore.RecordDiscovery(
			ctx,
			source,
			input,
		)
		if err != nil {
			t.Fatalf(
				"RecordDiscovery() error = %v, want nil",
				err,
			)
		}

		wantResult := discovery.RecordResult{
			Accepted: 2,
			Dropped:  2,
		}
		if result != wantResult {
			t.Errorf(
				"result = %#v, want %#v",
				result,
				wantResult,
			)
		}

		rows, err := pool.Query(
			ctx,
			`
				SELECT candidate_origin
				FROM discovery_edges
				WHERE source_origin = $1
					AND candidate_origin LIKE '%-new.example'
				ORDER BY candidate_origin
			`,
			source.String(),
		)
		if err != nil {
			t.Fatalf(
				"query admitted candidates: %v",
				err,
			)
		}
		defer rows.Close()

		var got []string
		for rows.Next() {
			var storedOrigin string
			if err := rows.Scan(
				&storedOrigin,
			); err != nil {
				t.Fatalf(
					"scan admitted candidate: %v",
					err,
				)
			}
			got = append(got, storedOrigin)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf(
				"iterate admitted candidates: %v",
				err,
			)
		}

		want := []string{
			"https://a-new.example",
			"https://b-new.example",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf(
				"admitted candidates = %#v, want %#v",
				got,
				want,
			)
		}
	})
}

func TestRecordDiscoveryConcurrentUpsertsRemainStable(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://example.com",
		now,
	)
	candidate := mustStoreOrigin(
		t,
		"https://example.net",
	)
	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	const recorderCount = 8

	start := make(chan struct{})
	errorsChannel := make(
		chan error,
		recorderCount,
	)

	var group sync.WaitGroup
	for range recorderCount {
		group.Add(1)

		go func() {
			defer group.Done()
			<-start

			_, err := discoveryStore.RecordDiscovery(
				ctx,
				source,
				[]discovery.Candidate{
					{
						Origin: candidate,
						Kind:   discovery.KindLink,
					},
				},
			)
			errorsChannel <- err
		}()
	}

	close(start)
	group.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Errorf(
				"RecordDiscovery() error = %v",
				err,
			)
		}
	}

	for table, want := range map[string]int{
		"discovery_candidates": 1,
		"discovery_edges":      1,
		"verification_queue":   1,
	} {
		var count int
		query := fmt.Sprintf(
			"SELECT count(*) FROM %s",
			pgx.Identifier{table}.Sanitize(),
		)
		err := pool.QueryRow(ctx, query).Scan(&count)
		if err != nil {
			t.Fatalf(
				"count %s: %v",
				table,
				err,
			)
		}

		if count != want {
			t.Errorf(
				"%s count = %d, want %d",
				table,
				count,
				want,
			)
		}
	}
}

func TestRecordDiscoveryRollsBackIncompleteEdgeBatch(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://example.com",
		now,
	)
	candidate := mustStoreOrigin(
		t,
		"https://example.net",
	)

	_, err := pool.Exec(
		ctx,
		`
			CREATE FUNCTION suppress_discovery_edge()
			RETURNS trigger
			LANGUAGE plpgsql
			AS $$
			BEGIN
				RETURN NULL;
			END
			$$;

			CREATE TRIGGER suppress_discovery_edge
			BEFORE INSERT ON discovery_edges
			FOR EACH ROW
			EXECUTE FUNCTION suppress_discovery_edge();
		`,
		pgx.QueryExecModeSimpleProtocol,
	)
	if err != nil {
		t.Fatalf(
			"install edge suppression trigger: %v",
			err,
		)
	}

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	_, err = discoveryStore.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{
			{
				Origin: candidate,
				Kind:   discovery.KindLink,
			},
		},
	)
	if err == nil {
		t.Fatal(
			"RecordDiscovery() error = nil, want non-nil",
		)
	}

	if !strings.Contains(
		err.Error(),
		"edge write was incomplete",
	) {
		t.Errorf(
			"RecordDiscovery() error = %v, want incomplete edge error",
			err,
		)
	}

	for _, table := range []string{
		"discovery_candidates",
		"discovery_edges",
		"verification_queue",
	} {
		var count int
		query := fmt.Sprintf(
			"SELECT count(*) FROM %s",
			pgx.Identifier{table}.Sanitize(),
		)
		err := pool.QueryRow(ctx, query).Scan(&count)
		if err != nil {
			t.Fatalf(
				"count %s: %v",
				table,
				err,
			)
		}

		if count != 0 {
			t.Errorf(
				"%s count = %d, want 0",
				table,
				count,
			)
		}
	}
}

func TestDiscoveryStoreValidatesInput(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://example.com",
		now,
	)
	candidate := mustStoreOrigin(
		t,
		"https://example.net",
	)
	validCandidate := discovery.Candidate{
		Origin: candidate,
		Kind:   discovery.KindLink,
	}

	if discoveryStore, err := NewDiscoveryStore(
		nil,
	); !errors.Is(err, errPoolUnavailable) ||
		discoveryStore != nil {
		t.Errorf(
			"NewDiscoveryStore(nil) = %#v, %v",
			discoveryStore,
			err,
		)
	}

	if discoveryStore, err := newDiscoveryStore(
		pool,
		nil,
	); !errors.Is(
		err,
		errDiscoveryClockUnavailable,
	) || discoveryStore != nil {
		t.Errorf(
			"newDiscoveryStore(nil clock) = %#v, %v",
			discoveryStore,
			err,
		)
	}

	var nilStore *DiscoveryStore

	_, _, err := nilStore.ClaimDiscoverySource(
		ctx,
		time.Hour,
	)
	if !errors.Is(err, errDiscoveryStoreUnavailable) {
		t.Errorf("nil store claim error = %v", err)
	}

	_, err = nilStore.RecordDiscovery(
		ctx,
		source,
		nil,
	)
	if !errors.Is(err, errDiscoveryStoreUnavailable) {
		t.Errorf("nil store record error = %v", err)
	}

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	_, _, err = discoveryStore.ClaimDiscoverySource(
		nil,
		time.Hour,
	)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf("nil context claim error = %v", err)
	}

	_, err = discoveryStore.RecordDiscovery(
		nil,
		source,
		nil,
	)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf("nil context record error = %v", err)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	_, _, err = discoveryStore.ClaimDiscoverySource(
		canceledContext,
		time.Hour,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled claim error = %v", err)
	}

	_, err = discoveryStore.RecordDiscovery(
		canceledContext,
		source,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled record error = %v", err)
	}

	_, _, err = discoveryStore.ClaimDiscoverySource(
		ctx,
		0,
	)
	if !errors.Is(err, errInvalidDiscoveryInterval) {
		t.Errorf("zero interval error = %v", err)
	}

	poolMissingStore := &DiscoveryStore{
		clock: &discoveryTestClock{
			times: []time.Time{now},
		},
	}
	_, _, err = poolMissingStore.ClaimDiscoverySource(
		ctx,
		time.Hour,
	)
	if !errors.Is(err, errPoolUnavailable) {
		t.Errorf("missing pool error = %v", err)
	}

	clockMissingStore := &DiscoveryStore{
		pool: pool,
	}
	_, _, err = clockMissingStore.
		ClaimDiscoverySource(ctx, time.Hour)
	if !errors.Is(
		err,
		errDiscoveryClockUnavailable,
	) {
		t.Errorf("missing clock error = %v", err)
	}

	tests := []struct {
		name       string
		source     origin.Origin
		candidates []discovery.Candidate
		want       error
	}{
		{
			name:       "zero source",
			candidates: []discovery.Candidate{validCandidate},
			want:       errInvalidOrigin,
		},
		{
			name:   "zero candidate",
			source: source,
			candidates: []discovery.Candidate{
				{
					Kind: discovery.KindLink,
				},
			},
			want: errInvalidDiscoveryCandidate,
		},
		{
			name:   "source candidate",
			source: source,
			candidates: []discovery.Candidate{
				{
					Origin: source,
					Kind:   discovery.KindLink,
				},
			},
			want: errInvalidDiscoveryCandidate,
		},
		{
			name:   "unknown kind",
			source: source,
			candidates: []discovery.Candidate{
				{
					Origin: candidate,
					Kind:   discovery.Kind(255),
				},
			},
			want: errInvalidDiscoveryCandidate,
		},
		{
			name:   "duplicate candidate",
			source: source,
			candidates: []discovery.Candidate{
				validCandidate,
				validCandidate,
			},
			want: errInvalidDiscoveryCandidate,
		},
		{
			name:   "oversized batch",
			source: source,
			candidates: make(
				[]discovery.Candidate,
				discovery.MaxCandidates+1,
			),
			want: errInvalidDiscoveryCandidate,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := discoveryStore.RecordDiscovery(
				ctx,
				test.source,
				test.candidates,
			)
			if !errors.Is(err, test.want) {
				t.Errorf(
					"RecordDiscovery() error = %v, want %v",
					err,
					test.want,
				)
			}
		})
	}

	result, err := discoveryStore.RecordDiscovery(
		ctx,
		source,
		nil,
	)
	if err != nil {
		t.Fatalf(
			"empty RecordDiscovery() error = %v",
			err,
		)
	}
	if result != (discovery.RecordResult{}) {
		t.Errorf(
			"empty RecordDiscovery() result = %#v",
			result,
		)
	}

	unknownSource := mustStoreOrigin(
		t,
		"https://unknown.example",
	)
	_, err = discoveryStore.RecordDiscovery(
		ctx,
		unknownSource,
		[]discovery.Candidate{validCandidate},
	)
	if !errors.Is(err, errDiscoverySourceUnknown) {
		t.Errorf(
			"unknown source error = %v, want %v",
			err,
			errDiscoverySourceUnknown,
		)
	}

	prepared, err := prepareDiscoveryCandidates(
		source,
		[]discovery.Candidate{
			{
				Origin: mustStoreOrigin(
					t,
					"https://z.example",
				),
				Kind: discovery.KindRedirect,
			},
			{
				Origin: mustStoreOrigin(
					t,
					"https://a.example",
				),
				Kind: discovery.KindLink,
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"prepareDiscoveryCandidates() error = %v",
			err,
		)
	}

	gotOrder := []string{
		prepared[0].Origin.String(),
		prepared[1].Origin.String(),
	}
	wantOrder := append([]string(nil), gotOrder...)
	sort.Strings(wantOrder)

	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Errorf(
			"prepared order = %#v, want %#v",
			gotOrder,
			wantOrder,
		)
	}

	if text, known := discoveryKindText(
		discovery.KindLink,
	); text != "link" || !known {
		t.Errorf(
			"link kind = %q, %v",
			text,
			known,
		)
	}

	if text, known := discoveryKindText(
		discovery.KindRedirect,
	); text != "redirect" || !known {
		t.Errorf(
			"redirect kind = %q, %v",
			text,
			known,
		)
	}

	if text, known := discoveryKindText(
		discovery.Kind(255),
	); text != "" || known {
		t.Errorf(
			"unknown kind = %q, %v",
			text,
			known,
		)
	}
}

func TestDiscoveryStoreReturnsClockAndDatabaseFailures(
	t *testing.T,
) {
	t.Run("claim clock", func(t *testing.T) {
		pool := newStoreTestPool(t)
		discoveryStore, err := newDiscoveryStore(
			pool,
			&discoveryTestClock{
				err: errDiscoveryTestClock,
			},
		)
		if err != nil {
			t.Fatalf(
				"newDiscoveryStore() error = %v",
				err,
			)
		}

		_, _, err =
			discoveryStore.ClaimDiscoverySource(
				context.Background(),
				time.Hour,
			)
		if !errors.Is(
			err,
			errDiscoveryTestClock,
		) {
			t.Errorf("claim clock error = %v", err)
		}
	})

	t.Run("record clock", func(t *testing.T) {
		pool := newStoreTestPool(t)
		now := queueTestTime()
		source := seedDiscoveryTestSource(
			t,
			pool,
			"https://example.com",
			now,
		)
		candidate := mustStoreOrigin(
			t,
			"https://example.net",
		)
		discoveryStore, err := newDiscoveryStore(
			pool,
			&discoveryTestClock{
				err: errDiscoveryTestClock,
			},
		)
		if err != nil {
			t.Fatalf(
				"newDiscoveryStore() error = %v",
				err,
			)
		}

		_, err = discoveryStore.RecordDiscovery(
			context.Background(),
			source,
			[]discovery.Candidate{
				{
					Origin: candidate,
					Kind:   discovery.KindLink,
				},
			},
		)
		if !errors.Is(
			err,
			errDiscoveryTestClock,
		) {
			t.Errorf("record clock error = %v", err)
		}
	})

	t.Run("invalid stored origin", func(t *testing.T) {
		ctx := context.Background()
		pool := newStoreTestPool(t)
		now := queueTestTime()

		_, err := pool.Exec(
			ctx,
			`
				INSERT INTO origins (
					origin,
					first_observed_at
				)
				VALUES (
					'not-an-origin',
					$1
				);

				INSERT INTO verification_observations (
					origin,
					observed_at,
					outcome,
					version,
					identity
				)
				VALUES (
					'not-an-origin',
					$1,
					'valid',
					1,
					'undeclared'
				);
			`,
			pgx.QueryExecModeSimpleProtocol,
			now,
		)
		if err != nil {
			t.Fatalf(
				"insert invalid stored origin: %v",
				err,
			)
		}

		discoveryStore := newDiscoveryTestStore(
			t,
			pool,
			now,
		)

		_, _, err =
			discoveryStore.ClaimDiscoverySource(
				ctx,
				time.Hour,
			)
		if err == nil ||
			!strings.Contains(
				err.Error(),
				"invalid discovery source",
			) {
			t.Errorf(
				"invalid stored origin error = %v",
				err,
			)
		}
	})

	t.Run("claim database", func(t *testing.T) {
		ctx := context.Background()
		pool := newStoreTestPool(t)
		now := queueTestTime()

		_, err := pool.Exec(
			ctx,
			"DROP TABLE origins CASCADE",
		)
		if err != nil {
			t.Fatalf("drop origins: %v", err)
		}

		discoveryStore := newDiscoveryTestStore(
			t,
			pool,
			now,
		)

		_, _, err =
			discoveryStore.ClaimDiscoverySource(
				ctx,
				time.Hour,
			)
		if err == nil {
			t.Fatal(
				"claim database error = nil, want non-nil",
			)
		}
	})

	t.Run("record database", func(t *testing.T) {
		ctx := context.Background()
		pool := newStoreTestPool(t)
		now := queueTestTime()
		source := seedDiscoveryTestSource(
			t,
			pool,
			"https://example.com",
			now,
		)
		candidate := mustStoreOrigin(
			t,
			"https://example.net",
		)

		_, err := pool.Exec(
			ctx,
			"DROP TABLE discovery_edges",
		)
		if err != nil {
			t.Fatalf(
				"drop discovery edges: %v",
				err,
			)
		}

		discoveryStore := newDiscoveryTestStore(
			t,
			pool,
			now,
		)

		_, err = discoveryStore.RecordDiscovery(
			ctx,
			source,
			[]discovery.Candidate{
				{
					Origin: candidate,
					Kind:   discovery.KindLink,
				},
			},
		)
		if err == nil {
			t.Fatal(
				"record database error = nil, want non-nil",
			)
		}
	})
}

type discoveryTestClock struct {
	mutex sync.Mutex
	times []time.Time
	err   error
}

func (clock *discoveryTestClock) NowTransaction(
	_ context.Context,
	_ pgx.Tx,
) (time.Time, error) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	if clock.err != nil {
		return time.Time{}, clock.err
	}

	if len(clock.times) == 0 {
		return time.Time{}, errDiscoveryTestClock
	}

	now := clock.times[0]
	if len(clock.times) > 1 {
		clock.times = clock.times[1:]
	}

	return now.UTC(), nil
}

type discoveryTestClaim struct {
	source origin.Origin
	found  bool
	err    error
}

type discoveryTestQueueState struct {
	mode          string
	availableAt   time.Time
	generation    int64
	owner         *string
	expiresAt     *time.Time
	lastClaimedAt *time.Time
}

func newDiscoveryTestStore(
	t *testing.T,
	pool *pgxpool.Pool,
	times ...time.Time,
) *DiscoveryStore {
	t.Helper()

	discoveryStore, err := newDiscoveryStore(
		pool,
		&discoveryTestClock{
			times: append(
				[]time.Time(nil),
				times...,
			),
		},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	return discoveryStore
}

func seedDiscoveryTestSource(
	t *testing.T,
	pool *pgxpool.Pool,
	rawOrigin string,
	observedAt time.Time,
) origin.Origin {
	t.Helper()

	source := mustStoreOrigin(t, rawOrigin)
	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		observedAt.Add(-time.Hour),
		declaration.OutcomeValid,
		declaration.IdentityUndeclared,
	)

	return source
}

func seedDiscoveryTestObservation(
	t *testing.T,
	pool *pgxpool.Pool,
	source origin.Origin,
	observedAt time.Time,
	outcome declaration.Outcome,
	identity declaration.Identity,
) {
	t.Helper()

	result := declaration.Result{
		Origin:  source,
		Outcome: outcome,
	}

	if outcome == declaration.OutcomeValid {
		result.Declaration = declaration.Declaration{
			Version:  1,
			Identity: identity,
		}
	}

	if err := New(pool).RecordVerification(
		context.Background(),
		observedAt,
		result,
	); err != nil {
		t.Fatalf(
			"seed RecordVerification() error = %v",
			err,
		)
	}
}

func seedDiscoveryTestHistory(
	t *testing.T,
	pool *pgxpool.Pool,
	source origin.Origin,
	count int,
	discoveredAt time.Time,
) []string {
	t.Helper()

	candidates := make([]string, count)
	for index := range count {
		candidates[index] = fmt.Sprintf(
			"https://existing-%04d.example",
			index,
		)
	}

	ctx := context.Background()

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			SELECT
				candidate.origin,
				$2,
				$2
			FROM unnest($1::text[]) AS candidate(origin)
		`,
		candidates,
		discoveredAt,
	)
	if err != nil {
		t.Fatalf(
			"seed discovery candidates: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_edges (
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			)
			SELECT
				$1,
				candidate.origin,
				'link',
				$3,
				$3
			FROM unnest($2::text[]) AS candidate(origin)
		`,
		source.String(),
		candidates,
		discoveredAt,
	)
	if err != nil {
		t.Fatalf(
			"seed discovery edges: %v",
			err,
		)
	}

	return candidates
}

func readDiscoveryTestQueueState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	source origin.Origin,
) discoveryTestQueueState {
	t.Helper()

	var state discoveryTestQueueState
	err := pool.QueryRow(
		ctx,
		`
			SELECT
				mode,
				available_at,
				lease_generation,
				lease_owner,
				lease_expires_at,
				last_claimed_at
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&state.mode,
		&state.availableAt,
		&state.generation,
		&state.owner,
		&state.expiresAt,
		&state.lastClaimedAt,
	)
	if err != nil {
		t.Fatalf(
			"query discovery queue state: %v",
			err,
		)
	}

	state.availableAt = state.availableAt.UTC()

	if state.expiresAt != nil {
		normalized := state.expiresAt.UTC()
		state.expiresAt = &normalized
	}

	if state.lastClaimedAt != nil {
		normalized := state.lastClaimedAt.UTC()
		state.lastClaimedAt = &normalized
	}

	return state
}

func discoveryTestOutcomeName(
	outcome declaration.Outcome,
) string {
	switch outcome {
	case declaration.OutcomeValid:
		return "valid"
	case declaration.OutcomeAbsent:
		return "absent"
	case declaration.OutcomeInvalid:
		return "invalid"
	case declaration.OutcomeUnsupportedVersion:
		return "unsupported_version"
	case declaration.OutcomeUnavailable:
		return "unavailable"
	case declaration.OutcomeRobotsDenied:
		return "robots_denied"
	case declaration.OutcomeCrossOriginRedirect:
		return "cross_origin_redirect"
	default:
		return "unknown"
	}
}
