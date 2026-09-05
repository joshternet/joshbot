package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
)

func TestRecordResultContainsNoHistoricalDropCount(
	t *testing.T,
) {
	resultType := reflect.TypeOf(
		discovery.RecordResult{},
	)

	if resultType.NumField() != 1 {
		t.Fatalf(
			"RecordResult field count = %d, want 1",
			resultType.NumField(),
		)
	}

	field := resultType.Field(0)
	if field.Name != "Accepted" {
		t.Errorf(
			"RecordResult field = %q, want Accepted",
			field.Name,
		)
	}

	if field.Type != reflect.TypeOf(int(0)) {
		t.Errorf(
			"RecordResult.Accepted type = %v, want int",
			field.Type,
		)
	}
}

func TestRecordDiscoveryPersistsEveryCandidateFromLargeSeedDirectory(
	t *testing.T,
) {
	const candidateCount = 2083

	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := mustStoreOrigin(
		t,
		"https://directory.example",
	)
	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	if err := discoveryStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	candidates := make(
		[]discovery.Candidate,
		0,
		candidateCount,
	)
	candidateOrigins := make(
		[]string,
		0,
		candidateCount,
	)

	for index := range candidateCount {
		candidate := mustStoreOrigin(
			t,
			fmt.Sprintf(
				"https://candidate-%04d.example",
				index,
			),
		)
		candidates = append(
			candidates,
			discovery.Candidate{
				Origin: candidate,
				Kind:   discovery.KindLink,
			},
		)
		candidateOrigins = append(
			candidateOrigins,
			candidate.String(),
		)
	}

	result, err := discoveryStore.RecordDiscovery(
		ctx,
		source,
		candidates,
	)
	if err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v, want nil",
			err,
		)
	}

	wantResult := discovery.RecordResult{
		Accepted: candidateCount,
	}
	if result != wantResult {
		t.Errorf(
			"RecordDiscovery() result = %#v, want %#v",
			result,
			wantResult,
		)
	}

	var (
		storedCandidateCount int
		storedEdgeCount      int
		probeCount           int
		nonProbeCount        int
		observationCount     int
		originCount          int
		sourceQueueCount     int
	)

	err = pool.QueryRow(
		ctx,
		`
			SELECT
				(
					SELECT count(*)
					FROM discovery_candidates
					WHERE origin = ANY($2::text[])
				),
				(
					SELECT count(*)
					FROM discovery_edges
					WHERE source_origin = $1
						AND candidate_origin =
							ANY($2::text[])
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = ANY($2::text[])
						AND mode = 'probe'
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = ANY($2::text[])
						AND mode <> 'probe'
				),
				(
					SELECT count(*)
					FROM verification_observations
				),
				(
					SELECT count(*)
					FROM origins
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = $1
				)
		`,
		source.String(),
		candidateOrigins,
	).Scan(
		&storedCandidateCount,
		&storedEdgeCount,
		&probeCount,
		&nonProbeCount,
		&observationCount,
		&originCount,
		&sourceQueueCount,
	)
	if err != nil {
		t.Fatalf(
			"query large discovery result: %v",
			err,
		)
	}

	if storedCandidateCount != candidateCount {
		t.Errorf(
			"stored candidate count = %d, want %d",
			storedCandidateCount,
			candidateCount,
		)
	}

	if storedEdgeCount != candidateCount {
		t.Errorf(
			"stored edge count = %d, want %d",
			storedEdgeCount,
			candidateCount,
		)
	}

	if probeCount != candidateCount {
		t.Errorf(
			"probe count = %d, want %d",
			probeCount,
			candidateCount,
		)
	}

	if nonProbeCount != 0 {
		t.Errorf(
			"non-probe queue count = %d, want 0",
			nonProbeCount,
		)
	}

	if observationCount != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			observationCount,
		)
	}

	if originCount != 0 {
		t.Errorf(
			"verification origin count = %d, want 0",
			originCount,
		)
	}

	if sourceQueueCount != 0 {
		t.Errorf(
			"seed queue count = %d, want 0",
			sourceQueueCount,
		)
	}

	var missingEdgeCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM unnest($2::text[])
				AS expected(candidate_origin)
			LEFT JOIN discovery_edges AS edge
				ON edge.source_origin = $1
				AND edge.candidate_origin =
					expected.candidate_origin
				AND edge.kind = 'link'
			WHERE edge.candidate_origin IS NULL
		`,
		source.String(),
		candidateOrigins,
	).Scan(&missingEdgeCount)
	if err != nil {
		t.Fatalf(
			"query missing discovery edges: %v",
			err,
		)
	}

	if missingEdgeCount != 0 {
		t.Errorf(
			"missing discovery edge count = %d, want 0",
			missingEdgeCount,
		)
	}

	var correctlyTimedCandidateCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_candidates
			WHERE origin = ANY($1::text[])
				AND first_discovered_at = $2
				AND last_discovered_at = $2
		`,
		candidateOrigins,
		now,
	).Scan(&correctlyTimedCandidateCount)
	if err != nil {
		t.Fatalf(
			"query candidate timestamps: %v",
			err,
		)
	}

	if correctlyTimedCandidateCount != candidateCount {
		t.Errorf(
			"correctly timed candidate count = %d, want %d",
			correctlyTimedCandidateCount,
			candidateCount,
		)
	}

	var correctlyTimedEdgeCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_edges
			WHERE source_origin = $1
				AND candidate_origin = ANY($2::text[])
				AND first_discovered_at = $3
				AND last_discovered_at = $3
		`,
		source.String(),
		candidateOrigins,
		now,
	).Scan(&correctlyTimedEdgeCount)
	if err != nil {
		t.Fatalf(
			"query edge timestamps: %v",
			err,
		)
	}

	if correctlyTimedEdgeCount != candidateCount {
		t.Errorf(
			"correctly timed edge count = %d, want %d",
			correctlyTimedEdgeCount,
			candidateCount,
		)
	}
}
