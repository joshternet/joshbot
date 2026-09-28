package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

type durableParticipationMetadata struct {
	firstParticipatedAt        *time.Time
	initialDeclarationVersion  *int
	initialDeclarationIdentity *string
	latestDeclarationCheckAt   *time.Time
	latestDeclarationOutcome   *string
}

func TestRecordVerificationMaintainsDurableParticipationMetadata(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	memory := New(pool)

	source := mustStoreOrigin(
		t,
		"https://participation.example",
	)

	base := time.Date(
		2026,
		time.September,
		27,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	if err := memory.RecordVerification(
		ctx,
		base,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
	); err != nil {
		t.Fatalf(
			"RecordVerification(absent) error = %v, want nil",
			err,
		)
	}

	metadata := readDurableParticipationMetadata(
		t,
		pool,
		source,
	)

	if metadata.firstParticipatedAt != nil {
		t.Fatalf(
			"first participated at = %v, want NULL",
			*metadata.firstParticipatedAt,
		)
	}

	if metadata.initialDeclarationVersion != nil {
		t.Fatalf(
			"initial declaration version = %d, want NULL",
			*metadata.initialDeclarationVersion,
		)
	}

	if metadata.initialDeclarationIdentity != nil {
		t.Fatalf(
			"initial declaration identity = %q, want NULL",
			*metadata.initialDeclarationIdentity,
		)
	}

	assertDurableParticipationTime(
		t,
		"latest declaration check at",
		metadata.latestDeclarationCheckAt,
		base,
	)

	assertDurableParticipationString(
		t,
		"latest declaration outcome",
		metadata.latestDeclarationOutcome,
		"absent",
	)

	firstRecordedParticipation := base.Add(
		2 * time.Hour,
	)

	if err := memory.RecordVerification(
		ctx,
		firstRecordedParticipation,
		declaration.Result{
			Outcome: declaration.OutcomeValid,
			Origin:  source,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordVerification(first valid) error = %v, want nil",
			err,
		)
	}

	latestCheck := base.Add(
		3 * time.Hour,
	)

	if err := memory.RecordVerification(
		ctx,
		latestCheck,
		declaration.Result{
			Outcome: declaration.OutcomeUnavailable,
			Origin:  source,
		},
	); err != nil {
		t.Fatalf(
			"RecordVerification(unavailable) error = %v, want nil",
			err,
		)
	}

	earlierParticipation := base.Add(
		time.Hour,
	)

	if err := memory.RecordVerification(
		ctx,
		earlierParticipation,
		declaration.Result{
			Outcome: declaration.OutcomeValid,
			Origin:  source,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityDeclined,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordVerification(earlier valid) error = %v, want nil",
			err,
		)
	}

	if err := memory.RecordVerification(
		ctx,
		latestCheck,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
	); err != nil {
		t.Fatalf(
			"RecordVerification(equal-time absent) error = %v, want nil",
			err,
		)
	}

	metadata = readDurableParticipationMetadata(
		t,
		pool,
		source,
	)

	assertDurableParticipationTime(
		t,
		"first participated at",
		metadata.firstParticipatedAt,
		earlierParticipation,
	)

	assertDurableParticipationInt(
		t,
		"initial declaration version",
		metadata.initialDeclarationVersion,
		1,
	)

	assertDurableParticipationString(
		t,
		"initial declaration identity",
		metadata.initialDeclarationIdentity,
		"declined",
	)

	assertDurableParticipationTime(
		t,
		"latest declaration check at",
		metadata.latestDeclarationCheckAt,
		latestCheck,
	)

	assertDurableParticipationString(
		t,
		"latest declaration outcome",
		metadata.latestDeclarationOutcome,
		"absent",
	)
}

func TestQueueCompletionMaintainsDurableParticipationMetadata(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	firstCompletedAt := fixture.claimed.Add(
		5 * time.Minute,
	)
	fixture.queue.clock = fixedQueueClock{
		now: firstCompletedAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"first CompleteVerification() error = %v, want nil",
			err,
		)
	}

	metadata := readDurableParticipationMetadata(
		t,
		fixture.pool,
		fixture.source,
	)

	assertDurableParticipationTime(
		t,
		"first participated at",
		metadata.firstParticipatedAt,
		firstCompletedAt,
	)

	assertDurableParticipationInt(
		t,
		"initial declaration version",
		metadata.initialDeclarationVersion,
		1,
	)

	assertDurableParticipationString(
		t,
		"initial declaration identity",
		metadata.initialDeclarationIdentity,
		"affirmed",
	)

	assertDurableParticipationTime(
		t,
		"latest declaration check at",
		metadata.latestDeclarationCheckAt,
		firstCompletedAt,
	)

	assertDurableParticipationString(
		t,
		"latest declaration outcome",
		metadata.latestDeclarationOutcome,
		"valid",
	)

	secondClaimAt := firstCompletedAt.Add(
		2 * time.Hour,
	)
	secondLease := claimParticipationQueueAt(
		t,
		fixture,
		"worker-b",
		secondClaimAt,
	)

	secondCompletedAt := secondClaimAt.Add(
		5 * time.Minute,
	)
	fixture.queue.clock = fixedQueueClock{
		now: secondCompletedAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		secondLease,
		declaration.Result{
			Outcome: declaration.OutcomeValid,
			Origin:  fixture.source,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityDeclined,
			},
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"second CompleteVerification() error = %v, want nil",
			err,
		)
	}

	metadata = readDurableParticipationMetadata(
		t,
		fixture.pool,
		fixture.source,
	)

	assertDurableParticipationTime(
		t,
		"preserved first participated at",
		metadata.firstParticipatedAt,
		firstCompletedAt,
	)

	assertDurableParticipationString(
		t,
		"preserved initial declaration identity",
		metadata.initialDeclarationIdentity,
		"affirmed",
	)

	assertDurableParticipationTime(
		t,
		"second latest declaration check at",
		metadata.latestDeclarationCheckAt,
		secondCompletedAt,
	)

	assertDurableParticipationString(
		t,
		"second latest declaration outcome",
		metadata.latestDeclarationOutcome,
		"valid",
	)

	thirdClaimAt := secondCompletedAt.Add(
		2 * time.Hour,
	)
	thirdLease := claimParticipationQueueAt(
		t,
		fixture,
		"worker-c",
		thirdClaimAt,
	)

	thirdCompletedAt := thirdClaimAt.Add(
		5 * time.Minute,
	)
	fixture.queue.clock = fixedQueueClock{
		now: thirdCompletedAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		thirdLease,
		declaration.Result{
			Outcome: declaration.OutcomeUnavailable,
			Origin:  fixture.source,
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"third CompleteVerification() error = %v, want nil",
			err,
		)
	}

	metadata = readDurableParticipationMetadata(
		t,
		fixture.pool,
		fixture.source,
	)

	assertDurableParticipationTime(
		t,
		"final first participated at",
		metadata.firstParticipatedAt,
		firstCompletedAt,
	)

	assertDurableParticipationInt(
		t,
		"final initial declaration version",
		metadata.initialDeclarationVersion,
		1,
	)

	assertDurableParticipationString(
		t,
		"final initial declaration identity",
		metadata.initialDeclarationIdentity,
		"affirmed",
	)

	assertDurableParticipationTime(
		t,
		"final latest declaration check at",
		metadata.latestDeclarationCheckAt,
		thirdCompletedAt,
	)

	assertDurableParticipationString(
		t,
		"final latest declaration outcome",
		metadata.latestDeclarationOutcome,
		"unavailable",
	)
}

func claimParticipationQueueAt(
	t *testing.T,
	fixture completionFixture,
	workerID string,
	claimedAt time.Time,
) Lease {
	t.Helper()

	fixture.queue.clock = fixedQueueClock{
		now: claimedAt,
	}

	lease, found, err := fixture.queue.Claim(
		fixture.ctx,
		workerID,
	)
	if err != nil {
		t.Fatalf(
			"Claim(%q) error = %v, want nil",
			workerID,
			err,
		)
	}

	if !found {
		t.Fatalf(
			"Claim(%q) found = false, want true",
			workerID,
		)
	}

	return lease
}

func readDurableParticipationMetadata(
	t *testing.T,
	pool *pgxpool.Pool,
	source origin.Origin,
) durableParticipationMetadata {
	t.Helper()

	var metadata durableParticipationMetadata

	err := pool.QueryRow(
		context.Background(),
		`
			SELECT
				first_participated_at,
				initial_declaration_version,
				initial_declaration_identity,
				latest_declaration_check_at,
				latest_declaration_check_outcome
			FROM origins
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&metadata.firstParticipatedAt,
		&metadata.initialDeclarationVersion,
		&metadata.initialDeclarationIdentity,
		&metadata.latestDeclarationCheckAt,
		&metadata.latestDeclarationOutcome,
	)
	if err != nil {
		t.Fatalf(
			"query durable participation metadata: %v",
			err,
		)
	}

	return metadata
}

func assertDurableParticipationTime(
	t *testing.T,
	name string,
	got *time.Time,
	want time.Time,
) {
	t.Helper()

	if got == nil {
		t.Fatalf(
			"%s = NULL, want %v",
			name,
			want,
		)
	}

	if !got.Equal(want) {
		t.Fatalf(
			"%s = %v, want %v",
			name,
			*got,
			want,
		)
	}
}

func assertDurableParticipationInt(
	t *testing.T,
	name string,
	got *int,
	want int,
) {
	t.Helper()

	if got == nil {
		t.Fatalf(
			"%s = NULL, want %d",
			name,
			want,
		)
	}

	if *got != want {
		t.Fatalf(
			"%s = %d, want %d",
			name,
			*got,
			want,
		)
	}
}

func assertDurableParticipationString(
	t *testing.T,
	name string,
	got *string,
	want string,
) {
	t.Helper()

	if got == nil {
		t.Fatalf(
			"%s = NULL, want %q",
			name,
			want,
		)
	}

	if *got != want {
		t.Fatalf(
			"%s = %q, want %q",
			name,
			*got,
			want,
		)
	}
}
