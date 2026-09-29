package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/declaration"
)

func TestStoreRegistryOriginsWithoutDatabase(
	t *testing.T,
) {
	location := time.FixedZone(
		"test",
		-7*60*60,
	)
	firstParticipatedAt := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		location,
	)
	latestCheckAt := firstParticipatedAt.Add(
		24 * time.Hour,
	)

	validOutcome := "valid"
	validVersion := 1
	currentIdentity := "declined"
	absentOutcome := "absent"

	memory := newStore(
		&storeFakePostgres{
			queryResults: []storeFakeQueryResult{
				{
					rows: newStoreFakeRows(
						[]any{
							"https://example.com",
							firstParticipatedAt,
							1,
							"affirmed",
							latestCheckAt,
							"unavailable",
							&validOutcome,
							&validVersion,
							&currentIdentity,
						},
						[]any{
							"https://example.org",
							firstParticipatedAt,
							1,
							"declined",
							latestCheckAt,
							"absent",
							&absentOutcome,
							nil,
							nil,
						},
					),
				},
			},
		},
	)

	registry, err := memory.RegistryOrigins(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RegistryOrigins() error = %v",
			err,
		)
	}

	if len(registry) != 2 {
		t.Fatalf(
			"RegistryOrigins() length = %d, want 2",
			len(registry),
		)
	}

	current := registry[0]

	if current.Origin.String() !=
		"https://example.com" {
		t.Errorf(
			"current origin = %q, want https://example.com",
			current.Origin,
		)
	}

	if !current.FirstParticipatedAt.Equal(
		firstParticipatedAt.UTC(),
	) {
		t.Errorf(
			"first participated at = %v, want %v",
			current.FirstParticipatedAt,
			firstParticipatedAt.UTC(),
		)
	}

	if current.InitialDeclaration !=
		(declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		}) {
		t.Errorf(
			"initial declaration = %#v",
			current.InitialDeclaration,
		)
	}

	if !current.LatestDeclarationCheckAt.Equal(
		latestCheckAt.UTC(),
	) {
		t.Errorf(
			"latest declaration check at = %v, want %v",
			current.LatestDeclarationCheckAt,
			latestCheckAt.UTC(),
		)
	}

	if current.LatestDeclarationCheckOutcome !=
		declaration.OutcomeUnavailable {
		t.Errorf(
			"latest declaration check outcome = %v, want unavailable",
			current.LatestDeclarationCheckOutcome,
		)
	}

	if current.CurrentDeclaration == nil {
		t.Fatal(
			"current declaration = nil, want valid declaration",
		)
	}

	if *current.CurrentDeclaration !=
		(declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityDeclined,
		}) {
		t.Errorf(
			"current declaration = %#v",
			*current.CurrentDeclaration,
		)
	}

	former := registry[1]

	if former.Origin.String() !=
		"https://example.org" {
		t.Errorf(
			"former origin = %q, want https://example.org",
			former.Origin,
		)
	}

	if former.LatestDeclarationCheckOutcome !=
		declaration.OutcomeAbsent {
		t.Errorf(
			"former latest declaration check outcome = %v, want absent",
			former.LatestDeclarationCheckOutcome,
		)
	}

	if former.CurrentDeclaration != nil {
		t.Errorf(
			"former current declaration = %#v, want nil",
			former.CurrentDeclaration,
		)
	}
}

func TestStoreRegistryOriginsValidationFailureWithoutDatabase(
	t *testing.T,
) {
	var nilStore *Store

	registry, err := nilStore.RegistryOrigins(
		context.Background(),
	)

	if registry != nil {
		t.Fatalf(
			"RegistryOrigins() = %#v, want nil",
			registry,
		)
	}

	if !errors.Is(
		err,
		errStoreUnavailable,
	) {
		t.Fatalf(
			"RegistryOrigins() error = %v, want %v",
			err,
			errStoreUnavailable,
		)
	}
}

func TestStoreRegistryOriginsQueryFailureWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test registry origin query failure",
	)

	memory := newStore(
		&storeFakePostgres{
			queryResults: []storeFakeQueryResult{
				{
					err: testErr,
				},
			},
		},
	)

	registry, err := memory.RegistryOrigins(
		context.Background(),
	)

	if registry != nil {
		t.Fatalf(
			"RegistryOrigins() = %#v, want nil",
			registry,
		)
	}

	if !errors.Is(
		err,
		testErr,
	) ||
		!strings.Contains(
			err.Error(),
			"store: query registry origins",
		) {
		t.Fatalf(
			"RegistryOrigins() error = %v",
			err,
		)
	}
}

func TestStoreRegistryOriginsCollectionFailureWithoutDatabase(
	t *testing.T,
) {
	memory := newStore(
		&storeFakePostgres{
			queryResults: []storeFakeQueryResult{
				{
					rows: newStoreFakeRows(
						[]any{
							"https://example.com",
						},
					),
				},
			},
		},
	)

	registry, err := memory.RegistryOrigins(
		context.Background(),
	)

	if registry != nil {
		t.Fatalf(
			"RegistryOrigins() = %#v, want nil",
			registry,
		)
	}

	if err == nil ||
		!strings.Contains(
			err.Error(),
			"store: collect registry origins",
		) {
		t.Fatalf(
			"RegistryOrigins() error = %v",
			err,
		)
	}
}

func TestScanRegistryOriginSuccessWithoutDatabase(
	t *testing.T,
) {
	t.Run(
		"current participant",
		func(t *testing.T) {
			row := validStoredRegistryOriginRow()

			got, err := scanRegistryOrigin(row)
			if err != nil {
				t.Fatalf(
					"scanRegistryOrigin() error = %v",
					err,
				)
			}

			if got.Origin.String() !=
				"https://example.com" {
				t.Errorf(
					"origin = %q",
					got.Origin,
				)
			}

			if got.InitialDeclaration !=
				(declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				}) {
				t.Errorf(
					"initial declaration = %#v",
					got.InitialDeclaration,
				)
			}

			if got.LatestDeclarationCheckOutcome !=
				declaration.OutcomeUnavailable {
				t.Errorf(
					"latest outcome = %v, want unavailable",
					got.LatestDeclarationCheckOutcome,
				)
			}

			if got.CurrentDeclaration == nil {
				t.Fatal(
					"current declaration = nil",
				)
			}

			if *got.CurrentDeclaration !=
				(declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityDeclined,
				}) {
				t.Errorf(
					"current declaration = %#v",
					*got.CurrentDeclaration,
				)
			}
		},
	)

	t.Run(
		"former participant",
		func(t *testing.T) {
			row := validStoredRegistryOriginRow()

			absent := "absent"
			row.latestOutcome = "absent"
			row.effectiveOutcome = &absent
			row.effectiveVersion = nil
			row.effectiveIdentity = nil

			got, err := scanRegistryOrigin(row)
			if err != nil {
				t.Fatalf(
					"scanRegistryOrigin() error = %v",
					err,
				)
			}

			if got.CurrentDeclaration != nil {
				t.Errorf(
					"current declaration = %#v, want nil",
					got.CurrentDeclaration,
				)
			}

			if got.LatestDeclarationCheckOutcome !=
				declaration.OutcomeAbsent {
				t.Errorf(
					"latest outcome = %v, want absent",
					got.LatestDeclarationCheckOutcome,
				)
			}
		},
	)
}

func TestScanRegistryOriginRejectsInvalidStoredValues(
	t *testing.T,
) {
	scanFailure := errors.New(
		"test registry scan failure",
	)

	_, err := scanRegistryOrigin(
		storedRegistryOriginRow{
			scanError: scanFailure,
		},
	)
	if !errors.Is(
		err,
		scanFailure,
	) {
		t.Fatalf(
			"scanRegistryOrigin() scan error = %v, want %v",
			err,
			scanFailure,
		)
	}

	tests := []struct {
		name         string
		mutate       func(*storedRegistryOriginRow)
		wantContains string
	}{
		{
			name: "invalid origin",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.origin = "invalid"
			},
			wantContains: "origin",
		},
		{
			name: "invalid initial version",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.initialVersion = 2
			},
			wantContains: "initial declaration",
		},
		{
			name: "invalid initial identity",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.initialIdentity = "unknown"
			},
			wantContains: "initial declaration",
		},
		{
			name: "invalid latest outcome",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.latestOutcome = "unknown"
			},
			wantContains: "latest declaration check outcome",
		},
		{
			name: "participation after latest check",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.firstParticipatedAt =
					row.latestCheckAt.Add(
						time.Second,
					)
			},
			wantContains: "participation times",
		},
		{
			name: "missing authoritative state",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.effectiveOutcome = nil
			},
			wantContains: "authoritative declaration state",
		},
		{
			name: "unknown authoritative outcome",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				unknown := "unknown"
				row.effectiveOutcome = &unknown
			},
			wantContains: "authoritative declaration outcome",
		},
		{
			name: "unavailable authoritative outcome",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				unavailable := "unavailable"
				row.effectiveOutcome = &unavailable
				row.effectiveVersion = nil
				row.effectiveIdentity = nil
			},
			wantContains: "authoritative declaration outcome",
		},
		{
			name: "robots denied authoritative outcome",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				robotsDenied := "robots_denied"
				row.effectiveOutcome = &robotsDenied
				row.effectiveVersion = nil
				row.effectiveIdentity = nil
			},
			wantContains: "authoritative declaration outcome",
		},
		{
			name: "missing current version",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.effectiveVersion = nil
			},
			wantContains: "current declaration",
		},
		{
			name: "missing current identity",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.effectiveIdentity = nil
			},
			wantContains: "current declaration",
		},
		{
			name: "invalid current version",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				version := 2
				row.effectiveVersion = &version
			},
			wantContains: "current declaration",
		},
		{
			name: "invalid current identity",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				identity := "unknown"
				row.effectiveIdentity = &identity
			},
			wantContains: "current declaration",
		},
		{
			name: "non-valid outcome with version",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				absent := "absent"
				version := 1
				row.effectiveOutcome = &absent
				row.effectiveVersion = &version
				row.effectiveIdentity = nil
			},
			wantContains: "non-valid authoritative declaration",
		},
		{
			name: "non-valid outcome with identity",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				absent := "absent"
				identity := "affirmed"
				row.effectiveOutcome = &absent
				row.effectiveVersion = nil
				row.effectiveIdentity = &identity
			},
			wantContains: "non-valid authoritative declaration",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				row := validStoredRegistryOriginRow()
				test.mutate(&row)

				got, err := scanRegistryOrigin(row)

				if got != (RegistryOrigin{}) {
					t.Errorf(
						"scanRegistryOrigin() = %#v, want zero",
						got,
					)
				}

				if !errors.Is(
					err,
					errInvalidStoredRegistryOrigin,
				) {
					t.Fatalf(
						"scanRegistryOrigin() error = %v, want %v",
						err,
						errInvalidStoredRegistryOrigin,
					)
				}

				if !strings.Contains(
					err.Error(),
					test.wantContains,
				) {
					t.Errorf(
						"scanRegistryOrigin() error = %q, want %q",
						err,
						test.wantContains,
					)
				}
			},
		)
	}
}

type storedRegistryOriginRow struct {
	origin              string
	firstParticipatedAt time.Time
	initialVersion      int
	initialIdentity     string
	latestCheckAt       time.Time
	latestOutcome       string
	effectiveOutcome    *string
	effectiveVersion    *int
	effectiveIdentity   *string
	scanError           error
}

func validStoredRegistryOriginRow() storedRegistryOriginRow {
	firstParticipatedAt := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.FixedZone("test", -7*60*60),
	)
	latestCheckAt := firstParticipatedAt.Add(
		24 * time.Hour,
	)
	effectiveOutcome := "valid"
	effectiveVersion := 1
	effectiveIdentity := "declined"

	return storedRegistryOriginRow{
		origin:              "https://example.com",
		firstParticipatedAt: firstParticipatedAt,
		initialVersion:      1,
		initialIdentity:     "affirmed",
		latestCheckAt:       latestCheckAt,
		latestOutcome:       "unavailable",
		effectiveOutcome:    &effectiveOutcome,
		effectiveVersion:    &effectiveVersion,
		effectiveIdentity:   &effectiveIdentity,
	}
}

func (row storedRegistryOriginRow) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (row storedRegistryOriginRow) Scan(
	destinations ...any,
) error {
	if row.scanError != nil {
		return row.scanError
	}

	return assignStoreScanValues(
		destinations,
		[]any{
			row.origin,
			row.firstParticipatedAt,
			row.initialVersion,
			row.initialIdentity,
			row.latestCheckAt,
			row.latestOutcome,
			row.effectiveOutcome,
			row.effectiveVersion,
			row.effectiveIdentity,
		},
	)
}

func (row storedRegistryOriginRow) Values() ([]any, error) {
	if row.scanError != nil {
		return nil, row.scanError
	}

	return []any{
		row.origin,
		row.firstParticipatedAt,
		row.initialVersion,
		row.initialIdentity,
		row.latestCheckAt,
		row.latestOutcome,
		row.effectiveOutcome,
		row.effectiveVersion,
		row.effectiveIdentity,
	}, nil
}

func (row storedRegistryOriginRow) RawValues() [][]byte {
	return nil
}
