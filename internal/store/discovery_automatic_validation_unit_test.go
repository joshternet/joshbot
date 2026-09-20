package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

var errAutomaticAdmissionBegin = errors.New(
	"test automatic admission begin failure",
)

type automaticAdmissionUnitDatabase struct {
	beginErr error
}

var _ database.Postgres = (*automaticAdmissionUnitDatabase)(nil)

func (database *automaticAdmissionUnitDatabase) Begin(
	context.Context,
) (pgx.Tx, error) {
	if database.beginErr != nil {
		return nil, database.beginErr
	}

	return nil, errUnexpectedDiscoveryUnitDatabaseCall
}

func (*automaticAdmissionUnitDatabase) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{},
		errUnexpectedDiscoveryUnitDatabaseCall
}

func (*automaticAdmissionUnitDatabase) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, errUnexpectedDiscoveryUnitDatabaseCall
}

func (*automaticAdmissionUnitDatabase) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return discoveryUnitRow{
		err: errUnexpectedDiscoveryUnitDatabaseCall,
	}
}

func TestAdmitAutomaticCandidatesWrapperWithoutDatabase(
	t *testing.T,
) {
	store := automaticAdmissionUnitStore(
		t,
		true,
		nil,
	)

	candidates := []discovery.Candidate{
		{
			Origin: mustStoreOrigin(
				t,
				"https://example.com",
			),
			Kind: discovery.KindLink,
		},
		{
			Origin: mustStoreOrigin(
				t,
				"https://example.org",
			),
			Kind: discovery.KindLink,
		},
	}

	err := store.AdmitAutomaticCandidates(
		context.Background(),
		0,
		candidates,
	)

	if !errors.Is(
		err,
		errInvalidCrawlRun,
	) {
		t.Fatalf(
			"AdmitAutomaticCandidates() error = %v, want %v",
			err,
			errInvalidCrawlRun,
		)
	}
}

func TestCompleteAutomaticCandidatesValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"nil store",
		func(t *testing.T) {
			var store *DiscoveryStore

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				nil,
			)

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"automatic crawling disabled",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				false,
				nil,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				0,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{},
					},
				},
			)

			if err != nil {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid run",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				nil,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				0,
				nil,
			)

			if !errors.Is(
				err,
				errInvalidCrawlRun,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errInvalidCrawlRun,
				)
			}
		},
	)

	t.Run(
		"invalid candidate",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				nil,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{},
					},
				},
			)

			if !errors.Is(
				err,
				errInvalidDiscoveryCandidate,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errInvalidDiscoveryCandidate,
				)
			}
		},
	)

	t.Run(
		"duplicate candidate",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				nil,
			)

			candidate := discovery.Candidate{
				Origin: mustStoreOrigin(
					t,
					"https://example.com",
				),
				Kind: discovery.KindLink,
			}

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: candidate,
					},
					{
						Candidate: candidate,
					},
				},
			)

			if !errors.Is(
				err,
				errInvalidDiscoveryCandidate,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errInvalidDiscoveryCandidate,
				)
			}
		},
	)

	t.Run(
		"non-link candidate",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				nil,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{
							Origin: mustStoreOrigin(
								t,
								"https://example.com",
							),
							Kind: discovery.KindRedirect,
						},
					},
				},
			)

			if !errors.Is(
				err,
				errInvalidDiscoveryCandidate,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errInvalidDiscoveryCandidate,
				)
			}
		},
	)

	t.Run(
		"unsupported failure category",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				nil,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{
							Origin: mustStoreOrigin(
								t,
								"https://example.com",
							),
							Kind: discovery.KindLink,
						},
						FailureCategory: retry.CategoryPolicyBlocked,
					},
				},
			)

			if !errors.Is(
				err,
				errInvalidDiscoveryCandidate,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errInvalidDiscoveryCandidate,
				)
			}
		},
	)

	t.Run(
		"unsafe address reaches transaction",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				errAutomaticAdmissionBegin,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{
							Origin: mustStoreOrigin(
								t,
								"https://example.com",
							),
							Kind: discovery.KindLink,
						},
						FailureCategory: retry.CategoryUnsafeAddress,
					},
				},
			)

			if !errors.Is(
				err,
				errAutomaticAdmissionBegin,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errAutomaticAdmissionBegin,
				)
			}
		},
	)

	t.Run(
		"transient failure reaches transaction",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				errAutomaticAdmissionBegin,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{
							Origin: mustStoreOrigin(
								t,
								"https://example.com",
							),
							Kind: discovery.KindLink,
						},
						FailureCategory: retry.CategoryDNS,
					},
				},
			)

			if !errors.Is(
				err,
				errAutomaticAdmissionBegin,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errAutomaticAdmissionBegin,
				)
			}
		},
	)

	t.Run(
		"successful decision reaches transaction",
		func(t *testing.T) {
			store := automaticAdmissionUnitStore(
				t,
				true,
				errAutomaticAdmissionBegin,
			)

			err := store.CompleteAutomaticCandidates(
				ctx,
				1,
				[]AutomaticCandidateResult{
					{
						Candidate: discovery.Candidate{
							Origin: mustStoreOrigin(
								t,
								"https://example.com",
							),
							Kind: discovery.KindLink,
						},
						FailureCategory: retry.CategoryNone,
					},
				},
			)

			if !errors.Is(
				err,
				errAutomaticAdmissionBegin,
			) {
				t.Fatalf(
					"CompleteAutomaticCandidates() error = %v, want %v",
					err,
					errAutomaticAdmissionBegin,
				)
			}
		},
	)
}

func automaticAdmissionUnitStore(
	t *testing.T,
	enabled bool,
	beginErr error,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStoreWithConfig(
		&automaticAdmissionUnitDatabase{
			beginErr: beginErr,
		},
		databaseQueueClock{},
		AutomaticCrawlConfig{
			Enabled: enabled,
		},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStoreWithConfig() error = %v",
			err,
		)
	}

	return store
}
