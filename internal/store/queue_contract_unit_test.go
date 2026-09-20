package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/testutil"
)

type queueContractClock struct {
	now time.Time
	err error
}

func (clock queueContractClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	return clock.now, clock.err
}

type queueContractTransactionClock struct {
	now            time.Time
	transactionNow time.Time
	err            error
	transactionErr error
}

func (clock queueContractTransactionClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	return clock.now, clock.err
}

func (clock queueContractTransactionClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.transactionNow,
		clock.transactionErr
}

func TestQueueConstructorsWithoutDatabase(
	t *testing.T,
) {
	config := validQueueContractConfig()

	if _, err := NewQueue(
		nil,
		config,
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"NewQueue(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	queue, err := NewQueue(
		new(pgxpool.Pool),
		config,
	)
	if err != nil {
		t.Fatalf(
			"NewQueue() error = %v",
			err,
		)
	}

	if queue == nil {
		t.Fatal(
			"NewQueue() = nil",
		)
	}

	if _, err := newQueue(
		nil,
		config,
		queueContractClock{},
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"newQueue(nil pool) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	if _, err := newQueue(
		new(pgxpool.Pool),
		QueueConfig{},
		queueContractClock{},
	); !errors.Is(
		err,
		errInvalidQueueConfig,
	) {
		t.Fatalf(
			"newQueue(invalid config) error = %v, want %v",
			err,
			errInvalidQueueConfig,
		)
	}

	if _, err := newQueue(
		new(pgxpool.Pool),
		config,
		nil,
	); !errors.Is(
		err,
		errQueueClockUnavailable,
	) {
		t.Fatalf(
			"newQueue(nil clock) error = %v, want %v",
			err,
			errQueueClockUnavailable,
		)
	}

	queue, err = newQueue(
		new(pgxpool.Pool),
		config,
		queueContractClock{},
	)
	if err != nil {
		t.Fatalf(
			"newQueue() error = %v",
			err,
		)
	}

	if queue == nil {
		t.Fatal(
			"newQueue() = nil",
		)
	}
}

func TestQueueValidateWithoutDatabase(
	t *testing.T,
) {
	config := validQueueContractConfig()

	var nilQueue *Queue

	if err := nilQueue.validate(
		context.Background(),
	); !errors.Is(
		err,
		errQueueUnavailable,
	) {
		t.Fatalf(
			"nil validate() error = %v, want %v",
			err,
			errQueueUnavailable,
		)
	}

	queue := &Queue{
		pool:   new(pgxpool.Pool),
		config: config,
		clock:  queueContractClock{},
	}

	if err := queue.validate(nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Fatalf(
			"validate(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := queue.validate(ctx); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"validate(canceled) error = %v, want %v",
			err,
			context.Canceled,
		)
	}

	if err := (&Queue{
		config: config,
		clock:  queueContractClock{},
	}).validate(
		context.Background(),
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"validate(nil pool) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	if err := (&Queue{
		pool:   new(pgxpool.Pool),
		config: QueueConfig{},
		clock:  queueContractClock{},
	}).validate(
		context.Background(),
	); !errors.Is(
		err,
		errInvalidQueueConfig,
	) {
		t.Fatalf(
			"validate(invalid config) error = %v, want %v",
			err,
			errInvalidQueueConfig,
		)
	}

	if err := (&Queue{
		pool:   new(pgxpool.Pool),
		config: config,
	}).validate(
		context.Background(),
	); !errors.Is(
		err,
		errQueueClockUnavailable,
	) {
		t.Fatalf(
			"validate(nil clock) error = %v, want %v",
			err,
			errQueueClockUnavailable,
		)
	}

	if err := queue.validate(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"validate() error = %v",
			err,
		)
	}
}

func TestQueueNowWithoutDatabase(
	t *testing.T,
) {
	pacific := time.FixedZone(
		"PDT",
		-7*60*60,
	)

	localTime := time.Date(
		2026,
		time.September,
		18,
		10,
		30,
		0,
		0,
		pacific,
	)

	queue := &Queue{
		pool: new(pgxpool.Pool),
		clock: queueContractClock{
			now: localTime,
		},
	}

	got, err := queue.now(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"now() error = %v",
			err,
		)
	}

	if !got.Equal(localTime.UTC()) {
		t.Fatalf(
			"now() = %v, want %v",
			got,
			localTime.UTC(),
		)
	}

	if got.Location() != time.UTC {
		t.Fatalf(
			"now() location = %v, want UTC",
			got.Location(),
		)
	}

	testErr := errors.New(
		"test queue clock failure",
	)

	queue.clock = queueContractClock{
		err: testErr,
	}

	got, err = queue.now(
		context.Background(),
	)

	if !got.IsZero() {
		t.Fatalf(
			"now() = %v, want zero time",
			got,
		)
	}

	if !errors.Is(
		err,
		testErr,
	) ||
		!strings.Contains(
			err.Error(),
			"store: read queue clock",
		) {
		t.Fatalf(
			"now() error = %v",
			err,
		)
	}
}

func TestQueueCompletionTimeWithoutDatabase(
	t *testing.T,
) {
	pacific := time.FixedZone(
		"PDT",
		-7*60*60,
	)

	localTime := time.Date(
		2026,
		time.September,
		18,
		10,
		30,
		0,
		0,
		pacific,
	)

	t.Run(
		"transaction clock",
		func(t *testing.T) {
			queue := &Queue{
				pool: new(pgxpool.Pool),
				clock: queueContractTransactionClock{
					transactionNow: localTime,
				},
			}

			got, err := queue.completionTime(
				context.Background(),
				nil,
			)
			if err != nil {
				t.Fatalf(
					"completionTime() error = %v",
					err,
				)
			}

			if !got.Equal(localTime.UTC()) {
				t.Fatalf(
					"completionTime() = %v, want %v",
					got,
					localTime.UTC(),
				)
			}

			if got.Location() != time.UTC {
				t.Fatalf(
					"completionTime() location = %v, want UTC",
					got.Location(),
				)
			}
		},
	)

	t.Run(
		"fallback clock",
		func(t *testing.T) {
			queue := &Queue{
				pool: new(pgxpool.Pool),
				clock: queueContractClock{
					now: localTime,
				},
			}

			got, err := queue.completionTime(
				context.Background(),
				nil,
			)
			if err != nil {
				t.Fatalf(
					"completionTime() error = %v",
					err,
				)
			}

			if !got.Equal(localTime.UTC()) {
				t.Fatalf(
					"completionTime() = %v, want %v",
					got,
					localTime.UTC(),
				)
			}

			if got.Location() != time.UTC {
				t.Fatalf(
					"completionTime() location = %v, want UTC",
					got.Location(),
				)
			}
		},
	)

	t.Run(
		"transaction clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test transaction clock failure",
			)

			queue := &Queue{
				pool: new(pgxpool.Pool),
				clock: queueContractTransactionClock{
					transactionErr: testErr,
				},
			}

			got, err := queue.completionTime(
				context.Background(),
				nil,
			)

			if !got.IsZero() {
				t.Fatalf(
					"completionTime() = %v, want zero time",
					got,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue completion clock",
				) {
				t.Fatalf(
					"completionTime() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"fallback clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test fallback clock failure",
			)

			queue := &Queue{
				pool: new(pgxpool.Pool),
				clock: queueContractClock{
					err: testErr,
				},
			}

			got, err := queue.completionTime(
				context.Background(),
				nil,
			)

			if !got.IsZero() {
				t.Fatalf(
					"completionTime() = %v, want zero time",
					got,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue completion clock",
				) {
				t.Fatalf(
					"completionTime() error = %v",
					err,
				)
			}
		},
	)
}

func TestQueueValidationHelpersWithoutDatabase(
	t *testing.T,
) {
	config := validQueueContractConfig()

	if !validQueueConfig(config) {
		t.Fatal(
			"validQueueConfig(valid) = false",
		)
	}

	invalidConfigs := []QueueConfig{
		{
			LeaseDuration:     0,
			MinOriginInterval: time.Second,
		},
		{
			LeaseDuration:     time.Second,
			MinOriginInterval: 0,
		},
		{
			LeaseDuration:     -time.Second,
			MinOriginInterval: time.Second,
		},
		{
			LeaseDuration:     time.Second,
			MinOriginInterval: -time.Second,
		},
	}

	for index, invalid := range invalidConfigs {
		if validQueueConfig(invalid) {
			t.Errorf(
				"validQueueConfig(invalid %d) = true",
				index,
			)
		}
	}

	validWorkerIDs := []string{
		"worker-1",
		strings.Repeat(
			"x",
			maxQueueWorkerIDLength,
		),
		strings.Repeat(
			"é",
			maxQueueWorkerIDLength,
		),
	}

	for _, workerID := range validWorkerIDs {
		if !validQueueWorkerID(workerID) {
			t.Errorf(
				"validQueueWorkerID(%q) = false",
				workerID,
			)
		}
	}

	invalidWorkerIDs := []string{
		"",
		strings.Repeat(
			"x",
			maxQueueWorkerIDLength+1,
		),
		strings.Repeat(
			"é",
			maxQueueWorkerIDLength+1,
		),
	}

	for _, workerID := range invalidWorkerIDs {
		if validQueueWorkerID(workerID) {
			t.Errorf(
				"validQueueWorkerID(%q) = true",
				workerID,
			)
		}
	}

	validLease := validQueueContractLease(t)

	if !validQueueLease(validLease) {
		t.Fatal(
			"validQueueLease(valid) = false",
		)
	}

	invalidLeases := []Lease{
		func() Lease {
			value := validLease
			value.Origin = origin.Origin{}
			return value
		}(),
		func() Lease {
			value := validLease
			value.WorkerID = ""
			return value
		}(),
		func() Lease {
			value := validLease
			value.Generation = 0
			return value
		}(),
		func() Lease {
			value := validLease
			value.ClaimedAt = time.Time{}
			return value
		}(),
		func() Lease {
			value := validLease
			value.ExpiresAt = time.Time{}
			return value
		}(),
		func() Lease {
			value := validLease
			value.ExpiresAt = value.ClaimedAt
			return value
		}(),
	}

	for index, invalid := range invalidLeases {
		if validQueueLease(invalid) {
			t.Errorf(
				"validQueueLease(invalid %d) = true",
				index,
			)
		}
	}

	if got := nullableFailureCategory(
		false,
		retry.CategoryDNS,
	); got != nil {
		t.Fatalf(
			"nullableFailureCategory(false) = %#v, want nil",
			got,
		)
	}

	if got := nullableFailureCategory(
		true,
		retry.CategoryDNS,
	); got != string(retry.CategoryDNS) {
		t.Fatalf(
			"nullableFailureCategory(true) = %#v, want %q",
			got,
			retry.CategoryDNS,
		)
	}
}

func TestReadQueueTimeWithoutDatabase(
	t *testing.T,
) {
	pacific := time.FixedZone(
		"PDT",
		-7*60*60,
	)

	localTime := time.Date(
		2026,
		time.September,
		18,
		10,
		30,
		0,
		0,
		pacific,
	)

	t.Run(
		"success",
		func(t *testing.T) {
			database := &storeFakePostgres{
				rowResults: []storeFakeRow{
					{
						values: []any{
							localTime,
						},
					},
				},
			}

			got, err := readQueueTime(
				context.Background(),
				database,
			)
			if err != nil {
				t.Fatalf(
					"readQueueTime() error = %v",
					err,
				)
			}

			if !got.Equal(localTime.UTC()) {
				t.Fatalf(
					"readQueueTime() = %v, want %v",
					got,
					localTime.UTC(),
				)
			}

			if got.Location() != time.UTC {
				t.Fatalf(
					"readQueueTime() location = %v, want UTC",
					got.Location(),
				)
			}
		},
	)

	t.Run(
		"failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue time failure",
			)

			database := &storeFakePostgres{
				rowResults: []storeFakeRow{
					{
						err: testErr,
					},
				},
			}

			got, err := readQueueTime(
				context.Background(),
				database,
			)

			if !got.IsZero() {
				t.Fatalf(
					"readQueueTime() = %v, want zero time",
					got,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"readQueueTime() error = %v, want %v",
					err,
					testErr,
				)
			}
		},
	)

	t.Run(
		"transaction clock",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							localTime,
						},
					},
				},
			}

			got, err := (databaseQueueClock{}).
				NowTransaction(
					context.Background(),
					tx,
				)
			if err != nil {
				t.Fatalf(
					"NowTransaction() error = %v",
					err,
				)
			}

			if !got.Equal(localTime.UTC()) {
				t.Fatalf(
					"NowTransaction() = %v, want %v",
					got,
					localTime.UTC(),
				)
			}

			if got.Location() != time.UTC {
				t.Fatalf(
					"NowTransaction() location = %v, want UTC",
					got.Location(),
				)
			}
		},
	)
}

func TestQueueContractGolden(
	t *testing.T,
) {
	var snapshot bytes.Buffer

	fmt.Fprintln(
		&snapshot,
		"=== config ===",
	)

	fmt.Fprintf(
		&snapshot,
		"valid=%t\n",
		validQueueConfig(
			validQueueContractConfig(),
		),
	)

	fmt.Fprintf(
		&snapshot,
		"zero-lease=%t\n",
		validQueueConfig(
			QueueConfig{
				MinOriginInterval: time.Second,
			},
		),
	)

	fmt.Fprintf(
		&snapshot,
		"zero-origin-interval=%t\n",
		validQueueConfig(
			QueueConfig{
				LeaseDuration: time.Second,
			},
		),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== worker IDs ===",
	)

	workerIDs := []struct {
		name  string
		value string
	}{
		{
			name:  "empty",
			value: "",
		},
		{
			name:  "normal",
			value: "worker-1",
		},
		{
			name:  "spaces",
			value: " worker ",
		},
		{
			name: "max-runes",
			value: strings.Repeat(
				"é",
				maxQueueWorkerIDLength,
			),
		},
		{
			name: "over-max-runes",
			value: strings.Repeat(
				"é",
				maxQueueWorkerIDLength+1,
			),
		},
	}

	for _, workerID := range workerIDs {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			workerID.name,
			validQueueWorkerID(workerID.value),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== lease ===",
	)

	validLease := validQueueContractLease(t)

	fmt.Fprintf(
		&snapshot,
		"valid=%t\n",
		validQueueLease(validLease),
	)

	zeroOrigin := validLease
	zeroOrigin.Origin = origin.Origin{}

	fmt.Fprintf(
		&snapshot,
		"zero-origin=%t\n",
		validQueueLease(zeroOrigin),
	)

	zeroGeneration := validLease
	zeroGeneration.Generation = 0

	fmt.Fprintf(
		&snapshot,
		"zero-generation=%t\n",
		validQueueLease(zeroGeneration),
	)

	nonForwardExpiration := validLease
	nonForwardExpiration.ExpiresAt =
		nonForwardExpiration.ClaimedAt

	fmt.Fprintf(
		&snapshot,
		"non-forward-expiration=%t\n",
		validQueueLease(nonForwardExpiration),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== failure category ===",
	)

	fmt.Fprintf(
		&snapshot,
		"non-transient=%v\n",
		nullableFailureCategory(
			false,
			retry.CategoryDNS,
		),
	)

	fmt.Fprintf(
		&snapshot,
		"transient=%v\n",
		nullableFailureCategory(
			true,
			retry.CategoryDNS,
		),
	)

	pacific := time.FixedZone(
		"PDT",
		-7*60*60,
	)

	localTime := time.Date(
		2026,
		time.September,
		18,
		10,
		30,
		0,
		0,
		pacific,
	)

	queue := &Queue{
		pool: new(pgxpool.Pool),
		clock: queueContractClock{
			now: localTime,
		},
	}

	queueNow, err := queue.now(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"now() error = %v",
			err,
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== clock ===",
	)

	fmt.Fprintf(
		&snapshot,
		"input=%s\n",
		localTime.Format(time.RFC3339),
	)

	fmt.Fprintf(
		&snapshot,
		"stored=%s\n",
		queueNow.Format(time.RFC3339),
	)

	if err := testutil.CheckGolden(
		"testdata/golden/queue-contract.golden",
		snapshot.Bytes(),
	); err != nil {
		t.Fatalf(
			"queue contract golden error = %v",
			err,
		)
	}
}

func validQueueContractConfig() QueueConfig {
	return QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: time.Hour,
	}
}

func validQueueContractLease(
	t *testing.T,
) Lease {
	t.Helper()

	claimedAt := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	return Lease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		WorkerID:   "worker-1",
		Generation: 7,
		ClaimedAt:  claimedAt,
		ExpiresAt: claimedAt.Add(
			10 * time.Minute,
		),
	}
}
