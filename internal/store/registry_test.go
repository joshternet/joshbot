package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestRegistryOriginsProjectsDurableParticipationState(
	t *testing.T,
) {
	memory := New(newStoreTestPool(t))

	base := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	neverParticipated := mustStoreOrigin(
		t,
		"https://example.edu",
	)
	current := mustStoreOrigin(
		t,
		"https://example.com",
	)
	former := mustStoreOrigin(
		t,
		"https://example.net",
	)
	returned := mustStoreOrigin(
		t,
		"https://example.org",
	)

	recordStoreResult(
		t,
		memory,
		base,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  neverParticipated,
		},
	)

	recordStoreResult(
		t,
		memory,
		base.Add(time.Hour),
		validStoreResult(
			current,
			declaration.IdentityAffirmed,
		),
	)

	recordStoreResult(
		t,
		memory,
		base.Add(2*time.Hour),
		validStoreResult(
			former,
			declaration.IdentityDeclined,
		),
	)

	recordStoreResult(
		t,
		memory,
		base.Add(3*time.Hour),
		validStoreResult(
			returned,
			declaration.IdentityAffirmed,
		),
	)

	t.Run(
		"temporary unavailable preserves current declaration",
		func(t *testing.T) {
			checkedAt := base.Add(
				4 * time.Hour,
			)

			recordStoreResult(
				t,
				memory,
				checkedAt,
				declaration.Result{
					Outcome: declaration.OutcomeUnavailable,
					Origin:  current,
				},
			)

			registry := readRegistryOrigins(
				t,
				memory,
			)

			got := findRegistryOrigin(
				t,
				registry,
				current.String(),
			)

			if got.LatestDeclarationCheckOutcome !=
				declaration.OutcomeUnavailable {
				t.Errorf(
					"latest declaration check outcome = %v, want unavailable",
					got.LatestDeclarationCheckOutcome,
				)
			}

			if !got.LatestDeclarationCheckAt.Equal(
				checkedAt,
			) {
				t.Errorf(
					"latest declaration check at = %v, want %v",
					got.LatestDeclarationCheckAt,
					checkedAt,
				)
			}

			assertRegistryCurrentDeclaration(
				t,
				got,
				declaration.IdentityAffirmed,
			)
		},
	)

	t.Run(
		"robots denial preserves current declaration",
		func(t *testing.T) {
			checkedAt := base.Add(
				5 * time.Hour,
			)

			recordStoreResult(
				t,
				memory,
				checkedAt,
				declaration.Result{
					Outcome: declaration.OutcomeRobotsDenied,
					Origin:  current,
				},
			)

			registry := readRegistryOrigins(
				t,
				memory,
			)

			got := findRegistryOrigin(
				t,
				registry,
				current.String(),
			)

			if got.LatestDeclarationCheckOutcome !=
				declaration.OutcomeRobotsDenied {
				t.Errorf(
					"latest declaration check outcome = %v, want robots denied",
					got.LatestDeclarationCheckOutcome,
				)
			}

			if !got.LatestDeclarationCheckAt.Equal(
				checkedAt,
			) {
				t.Errorf(
					"latest declaration check at = %v, want %v",
					got.LatestDeclarationCheckAt,
					checkedAt,
				)
			}

			assertRegistryCurrentDeclaration(
				t,
				got,
				declaration.IdentityAffirmed,
			)
		},
	)

	formerWithdrawalAt := base.Add(
		6 * time.Hour,
	)

	recordStoreResult(
		t,
		memory,
		formerWithdrawalAt,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  former,
		},
	)

	returnedWithdrawalAt := base.Add(
		7 * time.Hour,
	)

	recordStoreResult(
		t,
		memory,
		returnedWithdrawalAt,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  returned,
		},
	)

	t.Run(
		"withdrawal retains former participant without current declaration",
		func(t *testing.T) {
			registry := readRegistryOrigins(
				t,
				memory,
			)

			got := findRegistryOrigin(
				t,
				registry,
				former.String(),
			)

			if got.CurrentDeclaration != nil {
				t.Errorf(
					"current declaration = %#v, want nil",
					got.CurrentDeclaration,
				)
			}

			if got.LatestDeclarationCheckOutcome !=
				declaration.OutcomeAbsent {
				t.Errorf(
					"latest declaration check outcome = %v, want absent",
					got.LatestDeclarationCheckOutcome,
				)
			}

			if !got.LatestDeclarationCheckAt.Equal(
				formerWithdrawalAt,
			) {
				t.Errorf(
					"latest declaration check at = %v, want %v",
					got.LatestDeclarationCheckAt,
					formerWithdrawalAt,
				)
			}

			if got.InitialDeclaration !=
				(declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityDeclined,
				}) {
				t.Errorf(
					"initial declaration = %#v, want declined",
					got.InitialDeclaration,
				)
			}
		},
	)

	returnedAt := base.Add(
		8 * time.Hour,
	)

	recordStoreResult(
		t,
		memory,
		returnedAt,
		validStoreResult(
			returned,
			declaration.IdentityDeclined,
		),
	)

	t.Run(
		"later valid declaration restores current participation",
		func(t *testing.T) {
			registry := readRegistryOrigins(
				t,
				memory,
			)

			got := findRegistryOrigin(
				t,
				registry,
				returned.String(),
			)

			if got.FirstParticipatedAt !=
				base.Add(3*time.Hour) {
				t.Errorf(
					"first participated at = %v, want %v",
					got.FirstParticipatedAt,
					base.Add(3*time.Hour),
				)
			}

			if got.InitialDeclaration !=
				(declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				}) {
				t.Errorf(
					"initial declaration = %#v, want original affirmed declaration",
					got.InitialDeclaration,
				)
			}

			if got.LatestDeclarationCheckOutcome !=
				declaration.OutcomeValid {
				t.Errorf(
					"latest declaration check outcome = %v, want valid",
					got.LatestDeclarationCheckOutcome,
				)
			}

			if !got.LatestDeclarationCheckAt.Equal(
				returnedAt,
			) {
				t.Errorf(
					"latest declaration check at = %v, want %v",
					got.LatestDeclarationCheckAt,
					returnedAt,
				)
			}

			assertRegistryCurrentDeclaration(
				t,
				got,
				declaration.IdentityDeclined,
			)
		},
	)

	t.Run(
		"registry includes only origins that participated and orders canonically",
		func(t *testing.T) {
			registry := readRegistryOrigins(
				t,
				memory,
			)

			if len(registry) != 3 {
				t.Fatalf(
					"RegistryOrigins() length = %d, want 3",
					len(registry),
				)
			}

			wantOrigins := []string{
				"https://example.com",
				"https://example.net",
				"https://example.org",
			}

			for index, want := range wantOrigins {
				if registry[index].Origin.String() !=
					want {
					t.Errorf(
						"RegistryOrigins()[%d].Origin = %q, want %q",
						index,
						registry[index].Origin,
						want,
					)
				}
			}

			for _, entry := range registry {
				if entry.Origin ==
					neverParticipated {
					t.Errorf(
						"registry contains never-participated origin %q",
						neverParticipated,
					)
				}
			}
		},
	)
}

func TestRegistryOriginsIntegrationCoversFailureBoundaries(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"unavailable store",
		func(t *testing.T) {
			var memory *Store

			registry, err := memory.RegistryOrigins(ctx)

			if registry != nil {
				t.Fatalf(
					"RegistryOrigins() = %#v, want nil",
					registry,
				)
			}

			if !errors.Is(err, errStoreUnavailable) {
				t.Fatalf(
					"RegistryOrigins() error = %v, want %v",
					err,
					errStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"query failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test registry integration query failure",
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

			registry, err := memory.RegistryOrigins(ctx)

			if registry != nil {
				t.Fatalf(
					"RegistryOrigins() = %#v, want nil",
					registry,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: query registry origins",
				) {
				t.Fatalf(
					"RegistryOrigins() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collection failure",
		func(t *testing.T) {
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

			registry, err := memory.RegistryOrigins(ctx)

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
		},
	)
}

func TestScanRegistryOriginIntegrationRejectsMalformedRows(
	t *testing.T,
) {
	scanFailure := errors.New(
		"test registry integration scan failure",
	)

	_, err := scanRegistryOrigin(
		storedRegistryOriginRow{
			scanError: scanFailure,
		},
	)
	if !errors.Is(err, scanFailure) {
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
			name: "invalid initial declaration",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.initialVersion = 2
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
					row.latestCheckAt.Add(time.Second)
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
			name: "temporary authoritative outcome",
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
			name: "missing current declaration data",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				row.effectiveVersion = nil
			},
			wantContains: "current declaration",
		},
		{
			name: "invalid current declaration",
			mutate: func(
				row *storedRegistryOriginRow,
			) {
				version := 2
				row.effectiveVersion = &version
			},
			wantContains: "current declaration",
		},
		{
			name: "non-valid authoritative declaration data",
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

func readRegistryOrigins(
	t *testing.T,
	memory *Store,
) []RegistryOrigin {
	t.Helper()

	registry, err := memory.RegistryOrigins(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RegistryOrigins() error = %v, want nil",
			err,
		)
	}

	return registry
}

func findRegistryOrigin(
	t *testing.T,
	registry []RegistryOrigin,
	rawOrigin string,
) RegistryOrigin {
	t.Helper()

	for _, entry := range registry {
		if entry.Origin.String() ==
			rawOrigin {
			return entry
		}
	}

	t.Fatalf(
		"registry does not contain %q",
		rawOrigin,
	)

	return RegistryOrigin{}
}

func assertRegistryCurrentDeclaration(
	t *testing.T,
	entry RegistryOrigin,
	wantIdentity declaration.Identity,
) {
	t.Helper()

	if entry.CurrentDeclaration == nil {
		t.Fatal(
			"current declaration = nil, want declaration",
		)
	}

	want := declaration.Declaration{
		Version:  1,
		Identity: wantIdentity,
	}

	if *entry.CurrentDeclaration != want {
		t.Errorf(
			"current declaration = %#v, want %#v",
			*entry.CurrentDeclaration,
			want,
		)
	}
}
