package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestStoreVerifiedOriginsWithoutDatabase(
	t *testing.T,
) {
	store := newStore(
		&storeFakePostgres{
			queryResults: []storeFakeQueryResult{
				{
					rows: newStoreFakeRows(
						[]any{
							"https://example.com",
							1,
							"affirmed",
						},
					),
				},
			},
		},
	)

	verified, err := store.VerifiedOrigins(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v",
			err,
		)
	}

	if len(verified) != 1 {
		t.Fatalf(
			"VerifiedOrigins() length = %d, want 1",
			len(verified),
		)
	}

	if verified[0].Origin.String() !=
		"https://example.com" {
		t.Fatalf(
			"VerifiedOrigins()[0].Origin = %q",
			verified[0].Origin,
		)
	}

	if verified[0].Declaration.Version != 1 ||
		verified[0].Declaration.Identity !=
			declaration.IdentityAffirmed {
		t.Fatalf(
			"VerifiedOrigins()[0].Declaration = %#v",
			verified[0].Declaration,
		)
	}
}

func TestStoreVerifiedOriginsValidationFailureWithoutDatabase(
	t *testing.T,
) {
	store := newStore(nil)

	verified, err := store.VerifiedOrigins(
		context.Background(),
	)

	if verified != nil {
		t.Fatalf(
			"VerifiedOrigins() = %#v, want nil",
			verified,
		)
	}

	if !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"VerifiedOrigins() error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}
}

func TestStoreVerifiedOriginsQueryFailureWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test verified origin query failure",
	)

	store := newStore(
		&storeFakePostgres{
			queryResults: []storeFakeQueryResult{
				{
					err: testErr,
				},
			},
		},
	)

	verified, err := store.VerifiedOrigins(
		context.Background(),
	)

	if verified != nil {
		t.Fatalf(
			"VerifiedOrigins() = %#v, want nil",
			verified,
		)
	}

	if !errors.Is(
		err,
		testErr,
	) ||
		!strings.Contains(
			err.Error(),
			"store: query verified origins",
		) {
		t.Fatalf(
			"VerifiedOrigins() error = %v",
			err,
		)
	}
}

func TestStoreVerifiedOriginsCollectionFailureWithoutDatabase(
	t *testing.T,
) {
	store := newStore(
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

	verified, err := store.VerifiedOrigins(
		context.Background(),
	)

	if verified != nil {
		t.Fatalf(
			"VerifiedOrigins() = %#v, want nil",
			verified,
		)
	}

	if err == nil ||
		!strings.Contains(
			err.Error(),
			"store: collect verified origins",
		) {
		t.Fatalf(
			"VerifiedOrigins() error = %v",
			err,
		)
	}
}

func TestScanVerifiedOriginSuccessWithoutDatabase(
	t *testing.T,
) {
	got, err := scanVerifiedOrigin(
		storedVerifiedOriginRow{
			origin:   "https://example.com",
			version:  1,
			identity: "declined",
		},
	)
	if err != nil {
		t.Fatalf(
			"scanVerifiedOrigin() error = %v",
			err,
		)
	}

	if got.Origin.String() !=
		"https://example.com" {
		t.Fatalf(
			"scanVerifiedOrigin().Origin = %q",
			got.Origin,
		)
	}

	if got.Declaration.Version != 1 ||
		got.Declaration.Identity !=
			declaration.IdentityDeclined {
		t.Fatalf(
			"scanVerifiedOrigin().Declaration = %#v",
			got.Declaration,
		)
	}
}
