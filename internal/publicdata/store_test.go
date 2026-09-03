package publicdata

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

var publicDataSchemaSequence uint64

func TestStoreProjectionHasSemanticStabilityAndNoQueueLeakage(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newPublicDataTestPool(t)
	memory := store.New(pool)
	source := mustPublicDataOrigin(
		t,
		"https://example.com",
	)
	candidate := mustPublicDataOrigin(
		t,
		"https://example.net",
	)

	t1 := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	t4 := t3.Add(time.Hour)
	t5 := t4.Add(time.Hour)

	recordPublicDataResult(
		t,
		memory,
		t1,
		validPublicDataResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	snapshotA := buildStoreSnapshot(t, memory)

	recordPublicDataResult(
		t,
		memory,
		t2,
		declaration.Result{
			Outcome: declaration.OutcomeUnavailable,
			Origin:  source,
		},
	)
	snapshotB := buildStoreSnapshot(t, memory)

	if !reflect.DeepEqual(snapshotA, snapshotB) {
		t.Error(
			"temporary unavailable observation changed public snapshot",
		)
	}

	recordPublicDataResult(
		t,
		memory,
		t3,
		validPublicDataResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	snapshotC := buildStoreSnapshot(t, memory)

	if !reflect.DeepEqual(snapshotA, snapshotC) {
		t.Error(
			"same semantic re-verification changed public snapshot",
		)
	}

	recordPublicDataResult(
		t,
		memory,
		t4,
		validPublicDataResult(
			source,
			declaration.IdentityDeclined,
		),
	)
	snapshotD := buildStoreSnapshot(t, memory)

	if reflect.DeepEqual(snapshotA, snapshotD) {
		t.Error(
			"identity change did not change public snapshot",
		)
	}

	if publicDataNodePath(t, snapshotA) !=
		publicDataNodePath(t, snapshotD) {
		t.Error(
			"identity change changed node path",
		)
	}

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				last_attempted_at
			)
			VALUES ($1, $3);

			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			VALUES ($2, $3, $3);

			INSERT INTO discovery_edges (
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			)
			VALUES ($1, $2, 'link', $3, $3);

			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES ($2, $3, 'probe');
		`,
		pgx.QueryExecModeSimpleProtocol,
		source.String(),
		candidate.String(),
		t4,
	)
	if err != nil {
		t.Fatalf(
			"seed private discovery state: %v",
			err,
		)
	}

	snapshotE := buildStoreSnapshot(t, memory)
	if !reflect.DeepEqual(snapshotD, snapshotE) {
		t.Error(
			"private discovery state changed public snapshot",
		)
	}

	forbidden := []string{
		candidate.String(),
		"unavailable",
		"robots_denied",
		"lease_generation",
		"last_claimed_at",
		"lease_expires_at",
		"available_at",
		"observed_at",
		"first_observed_at",
		"last_attempted_at",
		"mode",
		"probe",
		"discovery",
		"candidate",
		"edge",
	}

	for _, file := range snapshotE {
		for _, value := range forbidden {
			if strings.Contains(
				string(file.Data),
				value,
			) {
				t.Errorf(
					"%s leaks %q",
					file.Path,
					value,
				)
			}
		}
	}

	recordPublicDataResult(
		t,
		memory,
		t5,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
	)
	removed := buildStoreSnapshot(t, memory)

	empty, err := Build(nil)
	if err != nil {
		t.Fatalf(
			"empty Build() error = %v, want nil",
			err,
		)
	}

	if !reflect.DeepEqual(removed, empty) {
		t.Errorf(
			"snapshot after absent = %#v, want %#v",
			removed,
			empty,
		)
	}
}

func TestDiscoveredCandidateBecomesPublicOnlyAfterValidProbe(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newPublicDataTestPool(t)
	memory := store.New(pool)
	source := mustPublicDataOrigin(
		t,
		"https://example.com",
	)
	candidate := mustPublicDataOrigin(
		t,
		"https://candidate.example",
	)

	recordPublicDataResult(
		t,
		memory,
		time.Date(
			2026,
			time.September,
			1,
			12,
			0,
			0,
			0,
			time.UTC,
		),
		validPublicDataResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	baseline := buildStoreSnapshot(t, memory)

	discoveryStore, err := store.NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	queue, err := store.NewQueue(
		pool,
		store.QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewQueue() error = %v, want nil",
			err,
		)
	}

	recordCandidate := func() {
		t.Helper()

		result, recordErr := discoveryStore.RecordDiscovery(
			ctx,
			source,
			[]discovery.Candidate{
				{
					Origin: candidate,
					Kind:   discovery.KindLink,
				},
			},
		)
		if recordErr != nil {
			t.Fatalf(
				"RecordDiscovery() error = %v, want nil",
				recordErr,
			)
		}

		want := discovery.RecordResult{Accepted: 1}
		if result != want {
			t.Errorf(
				"RecordDiscovery() result = %#v, want %#v",
				result,
				want,
			)
		}
	}

	claimCandidate := func(workerID string) store.Lease {
		t.Helper()

		lease, found, claimErr := queue.Claim(
			ctx,
			workerID,
		)
		if claimErr != nil {
			t.Fatalf(
				"Claim() error = %v, want nil",
				claimErr,
			)
		}

		if !found {
			t.Fatal("Claim() found = false, want true")
		}

		if lease.Origin != candidate {
			t.Fatalf(
				"Claim() origin = %q, want %q",
				lease.Origin,
				candidate,
			)
		}

		return lease
	}

	recordCandidate()
	assertPublicDataQueueMode(
		t,
		pool,
		candidate,
		"probe",
	)
	if got := buildStoreSnapshot(t, memory); !reflect.DeepEqual(
		got,
		baseline,
	) {
		t.Error("unverified candidate changed public snapshot")
	}

	absentLease := claimCandidate("candidate-probe-absent")
	err = queue.CompleteVerification(
		ctx,
		absentLease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  candidate,
		},
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf(
			"absent CompleteVerification() error = %v, want nil",
			err,
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
		t.Fatalf("count completed probe queue rows: %v", err)
	}

	if queueCount != 0 {
		t.Errorf(
			"completed absent probe queue rows = %d, want 0",
			queueCount,
		)
	}

	if got := buildStoreSnapshot(t, memory); !reflect.DeepEqual(
		got,
		baseline,
	) {
		t.Error("absent candidate changed public snapshot")
	}

	recordCandidate()
	validLease := claimCandidate("candidate-probe-valid")
	err = queue.CompleteVerification(
		ctx,
		validLease,
		validPublicDataResult(
			candidate,
			declaration.IdentityUndeclared,
		),
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf(
			"valid CompleteVerification() error = %v, want nil",
			err,
		)
	}

	assertPublicDataQueueMode(
		t,
		pool,
		candidate,
		"recurring",
	)

	publicSnapshot := buildStoreSnapshot(t, memory)
	if reflect.DeepEqual(publicSnapshot, baseline) {
		t.Fatal("valid candidate did not change public snapshot")
	}

	foundCandidate := false
	for _, file := range publicSnapshot {
		if strings.Contains(
			string(file.Data),
			candidate.String(),
		) {
			foundCandidate = true
			break
		}
	}

	if !foundCandidate {
		t.Errorf(
			"public snapshot does not contain valid candidate %q",
			candidate,
		)
	}
}

func assertPublicDataQueueMode(
	t *testing.T,
	pool *pgxpool.Pool,
	source origin.Origin,
	want string,
) {
	t.Helper()

	var got string
	err := pool.QueryRow(
		context.Background(),
		`
			SELECT mode
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&got)
	if err != nil {
		t.Fatalf("read queue mode: %v", err)
	}

	if got != want {
		t.Errorf(
			"queue mode = %q, want %q",
			got,
			want,
		)
	}
}

func newPublicDataTestPool(
	t *testing.T,
) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv(
		"JOSHBOT_TEST_DATABASE_URL",
	)
	if databaseURL == "" {
		if os.Getenv(
			"JOSHBOT_REQUIRE_DATABASE_TESTS",
		) == "1" {
			t.Fatal(
				"JOSHBOT_TEST_DATABASE_URL is required",
			)
		}

		t.Skip(
			"JOSHBOT_TEST_DATABASE_URL is not configured",
		)
	}

	adminConfig, err := pgxpool.ParseConfig(
		databaseURL,
	)
	if err != nil {
		t.Fatal(
			"invalid test database configuration",
		)
	}

	adminPool, err := pgxpool.NewWithConfig(
		context.Background(),
		adminConfig,
	)
	if err != nil {
		t.Fatal(
			"cannot create test database pool",
		)
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(
		context.Background(),
	); err != nil {
		t.Fatal(
			"cannot reach configured test database",
		)
	}

	schema := fmt.Sprintf(
		"publicdata_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&publicDataSchemaSequence,
			1,
		),
	)
	_, err = adminPool.Exec(
		context.Background(),
		"CREATE SCHEMA "+
			pgx.Identifier{schema}.Sanitize(),
	)
	if err != nil {
		t.Fatalf(
			"create isolated test schema: %v",
			err,
		)
	}

	testConfig, err := pgxpool.ParseConfig(
		databaseURL,
	)
	if err != nil {
		t.Fatal(
			"invalid test database configuration",
		)
	}
	testConfig.ConnConfig.RuntimeParams["search_path"] =
		schema

	testPool, err := pgxpool.NewWithConfig(
		context.Background(),
		testConfig,
	)
	if err != nil {
		t.Fatal(
			"cannot create isolated test database pool",
		)
	}

	t.Cleanup(func() {
		testPool.Close()

		_, cleanupErr := adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+
				pgx.Identifier{schema}.Sanitize()+
				" CASCADE",
		)
		if cleanupErr != nil {
			t.Errorf(
				"drop isolated test schema: %v",
				cleanupErr,
			)
		}
	})

	migrationFiles := []string{
		"../store/migrations/0001_initial.sql",
		"../store/migrations/0002_verification_queue.sql",
		"../store/migrations/0003_discovery.sql",
	}

	for _, migrationFile := range migrationFiles {
		migration, readErr := os.ReadFile(
			migrationFile,
		)
		if readErr != nil {
			t.Fatalf(
				"read migration %q: %v",
				migrationFile,
				readErr,
			)
		}

		_, execErr := testPool.Exec(
			context.Background(),
			string(migration),
			pgx.QueryExecModeSimpleProtocol,
		)
		if execErr != nil {
			t.Fatalf(
				"apply migration %q: %v",
				migrationFile,
				execErr,
			)
		}
	}

	return testPool
}

func mustPublicDataOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawURL,
			err,
		)
	}

	return source
}

func validPublicDataResult(
	source origin.Origin,
	identity declaration.Identity,
) declaration.Result {
	return declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: identity,
		},
	}
}

func recordPublicDataResult(
	t *testing.T,
	memory *store.Store,
	observedAt time.Time,
	result declaration.Result,
) {
	t.Helper()

	if err := memory.RecordVerification(
		context.Background(),
		observedAt,
		result,
	); err != nil {
		t.Fatalf(
			"RecordVerification() error = %v, want nil",
			err,
		)
	}
}

func buildStoreSnapshot(
	t *testing.T,
	memory *store.Store,
) []File {
	t.Helper()

	verified, err := memory.VerifiedOrigins(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v, want nil",
			err,
		)
	}

	snapshot, err := Build(verified)
	if err != nil {
		t.Fatalf(
			"Build() error = %v, want nil",
			err,
		)
	}

	return snapshot
}

func publicDataNodePath(
	t *testing.T,
	files []File,
) string {
	t.Helper()

	for _, file := range files {
		if strings.HasPrefix(
			file.Path,
			"nodes/",
		) {
			return file.Path
		}
	}

	t.Fatal("snapshot does not contain a node")

	return ""
}
