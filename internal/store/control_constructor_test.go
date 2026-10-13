package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInternalControlStoreRejectsUnavailablePool(
	t *testing.T,
) {
	controlStore, err := newControlStore(nil)

	if controlStore != nil {
		t.Fatalf(
			"newControlStore(nil) = %#v, want nil",
			controlStore,
		)
	}

	if !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"newControlStore(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}
}

func TestControlStoreExcludedHostConfigurationWithoutDatabase(t *testing.T) {
	pool := new(pgxpool.Pool)
	for _, hosts := range []string{"", "excluded.example,blogspot.*"} {
		if err := ValidateCrawlExcludedHosts(hosts); err != nil {
			t.Fatalf("valid excluded hosts %q rejected: %v", hosts, err)
		}
	}
	if err := ValidateCrawlExcludedHosts("https://invalid.example"); err == nil {
		t.Fatal("excluded host URL accepted")
	}
	if storage, err := NewControlStoreWithExcludedHosts(nil, ""); storage != nil || !errors.Is(err, errPoolUnavailable) {
		t.Fatalf("unavailable pool returned storage=%#v error=%v", storage, err)
	}
	if storage, err := NewControlStoreWithExcludedHosts(pool, "https://invalid.example"); storage != nil || err == nil || !strings.Contains(err.Error(), "excluded crawl hosts") {
		t.Fatalf("invalid excluded hosts returned storage=%#v error=%v", storage, err)
	}
	storage, err := NewControlStoreWithExcludedHosts(pool, "excluded.example")
	if err != nil || storage == nil {
		t.Fatalf("configured constructor returned storage=%#v error=%v", storage, err)
	}
	if storage.pool != pool {
		t.Fatal("configured constructor replaced the caller-owned pool")
	}
	// Exercise the constructed policy against an allow request. A configured
	// exclusion must keep an automatic source blocked even without a DB rule.
	tx := &crawlBlockGoldenTx{
		rows:    []storeFakeRow{{values: []any{false, true}}},
		queries: []storeFakeQueryResult{{rows: newStoreFakeRows()}},
	}
	if err := setExactCrawlBlockTransaction(context.Background(), tx,
		mustStoreOrigin(t, "https://host.excluded.example"), false, storage.automatic); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"intent source=https://host.excluded.example blocked=false",
		"update source=https://host.excluded.example blocked=true",
	}
	if !reflect.DeepEqual(tx.operations, want) {
		t.Fatalf("configured allow operations=%v, want %v", tx.operations, want)
	}
}
