package reporting

import (
	"errors"
	"testing"
)

func TestInternalPostgresReaderRejectsUnavailablePool(
	t *testing.T,
) {
	reader, err := newPostgresReader(
		nil,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)

	if reader != nil {
		t.Fatalf(
			"newPostgresReader(nil) = %#v, want nil",
			reader,
		)
	}

	if !errors.Is(
		err,
		errPostgresPoolUnavailable,
	) {
		t.Fatalf(
			"newPostgresReader(nil) error = %v, want %v",
			err,
			errPostgresPoolUnavailable,
		)
	}
}
