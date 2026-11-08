package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRemoveRegexExcludedOriginsWithoutDatabase(t *testing.T) {
	ctx := context.Background()

	t.Run("skips invalid origins and unmatched hosts", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{}
		err := removeRegexExcludedOrigins(
			ctx,
			tx,
			AutomaticCrawlConfig{
				ExcludedHostSuffixes: "re:^spam",
			},
			[]string{
				"ftp://invalid.example",
				"https://safe.example",
			},
		)
		if err != nil {
			t.Fatalf("removeRegexExcludedOrigins() error = %v", err)
		}
	})

	t.Run("deletes matching probe origins", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{
					tag: pgconn.NewCommandTag("DELETE 1"),
				},
			},
		}
		err := removeRegexExcludedOrigins(
			ctx,
			tx,
			AutomaticCrawlConfig{
				ExcludedHostSuffixes: "re:^spam",
			},
			[]string{"https://spam.example"},
		)
		if err != nil {
			t.Fatalf("removeRegexExcludedOrigins() error = %v", err)
		}
	})

	t.Run("delete failure", func(t *testing.T) {
		testErr := errors.New("test regex exclude delete failure")
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{err: testErr},
			},
		}
		err := removeRegexExcludedOrigins(
			ctx,
			tx,
			AutomaticCrawlConfig{
				ExcludedHostSuffixes: "re:^spam",
			},
			[]string{"https://spam.example"},
		)
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"remove regex-excluded discovery probes",
			) {
			t.Fatalf("removeRegexExcludedOrigins() error = %v", err)
		}
	})
}
