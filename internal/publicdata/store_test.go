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

	queue, err := store.NewQueue(
		pool,
		store.QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Nanosecond,
		},
	)
	if err != nil {
		t.Fatalf("NewQueue() error = %v, want nil", err)
	}

	if err := queue.Schedule(
		ctx,
		source,
		t1,
	); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(
		ctx,
		"private-worker-7",
	)
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	if lease.WorkerID != "private-worker-7" {
		t.Errorf(
			"lease worker = %q, want %q",
			lease.WorkerID,
			"private-worker-7",
		)
	}

	snapshotE := buildStoreSnapshot(t, memory)
	if !reflect.DeepEqual(snapshotD, snapshotE) {
		t.Error(
			"queue scheduling and claim changed public snapshot",
		)
	}

	forbidden := []string{
		"private-worker-7",
		"unavailable",
		"robots_denied",
		"lease_generation",
		"last_claimed_at",
		"lease_expires_at",
		"available_at",
		"observed_at",
		"first_observed_at",
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

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
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

	testConfig, err := pgxpool.ParseConfig(databaseURL)
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

		_, cleanupError := adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+
				pgx.Identifier{schema}.Sanitize()+
				" CASCADE",
		)
		if cleanupError != nil {
			t.Errorf(
				"drop isolated test schema: %v",
				cleanupError,
			)
		}
	})

	migrationFiles := []string{
		"../store/migrations/0001_initial.sql",
		"../store/migrations/0002_verification_queue.sql",
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
		if strings.HasPrefix(file.Path, "nodes/") {
			return file.Path
		}
	}

	t.Fatal("snapshot does not contain a node")

	return ""
}
