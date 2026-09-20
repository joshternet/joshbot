package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/discovery"
)

type discoveryLeaseRenewCaptureTx struct {
	*discoveryPolicyUnitTx

	renewQuery string
	renewArgs  []any
}

func (tx *discoveryLeaseRenewCaptureTx) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	if strings.Contains(
		query,
		"UPDATE discovery_source_state",
	) &&
		strings.Contains(
			query,
			"SET lease_expires_at = GREATEST",
		) {
		tx.renewQuery = query
		tx.renewArgs = append(
			[]any(nil),
			args...,
		)
	}

	return tx.discoveryPolicyUnitTx.QueryRow(
		ctx,
		query,
		args...,
	)
}

func TestRenewDiscoverySourceLeaseWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		19,
		22,
		30,
		0,
		0,
		time.UTC,
	)
	leaseDuration := 5 * time.Minute

	lease := discovery.CrawlSourceLease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Generation: 7,
		ClaimedAt:  now.Add(-5 * time.Minute),
		ExpiresAt:  now.Add(5 * time.Minute),
	}

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					leaseDuration,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid lease",
		func(t *testing.T) {
			store := claimDiscoveryUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
				claimDiscoveryClock{
					now: now,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					discovery.CrawlSourceLease{},
					leaseDuration,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoverySourceLease,
			) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v, want %v",
					err,
					errInvalidDiscoverySourceLease,
				)
			}
		},
	)

	t.Run(
		"invalid lease duration",
		func(t *testing.T) {
			store := claimDiscoveryUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
				claimDiscoveryClock{
					now: now,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					0,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoveryLeaseDuration,
			) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v, want %v",
					err,
					errInvalidDiscoveryLeaseDuration,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			store := claimDiscoveryUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              errDiscoveryPolicyBegin,
				},
				claimDiscoveryClock{
					now: now,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					leaseDuration,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: renew discovery source lease",
				) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease renewal clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					err: testErr,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					leaseDuration,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: read discovery clock",
				) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: pgx.ErrNoRows,
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					leaseDuration,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				ErrDiscoverySourceLeaseLost,
			) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v, want %v",
					err,
					ErrDiscoverySourceLeaseLost,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease renewal database failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					leaseDuration,
				)

			if renewed != (discovery.CrawlSourceLease{}) {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: update discovery source lease",
				) ||
				!strings.Contains(
					err.Error(),
					"store: renew discovery source lease",
				) {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			location := time.FixedZone(
				"test-offset",
				-7*60*60,
			)
			claimedAt := now.Add(
				-5 * time.Minute,
			).In(location)
			expiresAt := now.Add(
				10 * time.Minute,
			).In(location)

			baseTx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							int64(7),
							claimedAt,
							expiresAt,
						},
					},
				},
			}
			tx := &discoveryLeaseRenewCaptureTx{
				discoveryPolicyUnitTx: baseTx,
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					lease,
					leaseDuration,
				)
			if err != nil {
				t.Fatalf(
					"RenewDiscoverySourceLease() error = %v",
					err,
				)
			}

			want := discovery.CrawlSourceLease{
				Origin:     lease.Origin,
				Generation: 7,
				ClaimedAt:  claimedAt.UTC(),
				ExpiresAt:  expiresAt.UTC(),
			}

			if renewed != want {
				t.Fatalf(
					"RenewDiscoverySourceLease() lease = %#v, want %#v",
					renewed,
					want,
				)
			}

			normalizedQuery := strings.Join(
				strings.Fields(tx.renewQuery),
				" ",
			)

			if !strings.Contains(
				normalizedQuery,
				"SET lease_expires_at = GREATEST( lease_expires_at, $4 )",
			) {
				t.Fatalf(
					"renew query may shorten lease: %s",
					normalizedQuery,
				)
			}

			if !strings.Contains(
				normalizedQuery,
				"AND lease_generation = $2 AND lease_expires_at > $3",
			) {
				t.Fatalf(
					"renew query does not require current unexpired generation: %s",
					normalizedQuery,
				)
			}

			if len(tx.renewArgs) != 4 {
				t.Fatalf(
					"renew argument count = %d, want 4",
					len(tx.renewArgs),
				)
			}

			if got, ok := tx.renewArgs[0].(string); !ok ||
				got != lease.Origin.String() {
				t.Fatalf(
					"renew origin argument = %#v, want %q",
					tx.renewArgs[0],
					lease.Origin.String(),
				)
			}

			if got, ok := tx.renewArgs[1].(int64); !ok ||
				got != lease.Generation {
				t.Fatalf(
					"renew generation argument = %#v, want %d",
					tx.renewArgs[1],
					lease.Generation,
				)
			}

			if got, ok := tx.renewArgs[2].(time.Time); !ok ||
				!got.Equal(now) {
				t.Fatalf(
					"renew now argument = %#v, want %v",
					tx.renewArgs[2],
					now,
				)
			}

			wantCandidateExpiry := now.Add(
				leaseDuration,
			)
			if got, ok := tx.renewArgs[3].(time.Time); !ok ||
				!got.Equal(wantCandidateExpiry) {
				t.Fatalf(
					"renew expiration argument = %#v, want %v",
					tx.renewArgs[3],
					wantCandidateExpiry,
				)
			}
		},
	)
}

func TestValidDiscoverySourceLease(
	t *testing.T,
) {
	now := time.Date(
		2026,
		time.September,
		19,
		22,
		30,
		0,
		0,
		time.UTC,
	)

	valid := discovery.CrawlSourceLease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Generation: 1,
		ClaimedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}

	if !validDiscoverySourceLease(valid) {
		t.Fatal(
			"validDiscoverySourceLease(valid) = false, want true",
		)
	}

	tests := []struct {
		name  string
		lease discovery.CrawlSourceLease
	}{
		{
			name:  "empty origin",
			lease: discovery.CrawlSourceLease{},
		},
		{
			name: "zero generation",
			lease: discovery.CrawlSourceLease{
				Origin:    valid.Origin,
				ClaimedAt: valid.ClaimedAt,
				ExpiresAt: valid.ExpiresAt,
			},
		},
		{
			name: "zero claim time",
			lease: discovery.CrawlSourceLease{
				Origin:     valid.Origin,
				Generation: valid.Generation,
				ExpiresAt:  valid.ExpiresAt,
			},
		},
		{
			name: "zero expiration",
			lease: discovery.CrawlSourceLease{
				Origin:     valid.Origin,
				Generation: valid.Generation,
				ClaimedAt:  valid.ClaimedAt,
			},
		},
		{
			name: "expiration equals claim",
			lease: discovery.CrawlSourceLease{
				Origin:     valid.Origin,
				Generation: valid.Generation,
				ClaimedAt:  valid.ClaimedAt,
				ExpiresAt:  valid.ClaimedAt,
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				if validDiscoverySourceLease(
					test.lease,
				) {
					t.Fatalf(
						"validDiscoverySourceLease(%#v) = true, want false",
						test.lease,
					)
				}
			},
		)
	}
}
