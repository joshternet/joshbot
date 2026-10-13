package reporting

import (
	"context"
	"errors"
	"testing"
)

func TestCrawlDetailPaginationHTTPIntegration(t *testing.T) {
	t.Run("legacy reader", TestCrawlDetailProvidesBoundedContinuation)
	t.Run("paged reader", TestCrawlDetailPaginationQueriesPagedReader)
	t.Run("invalid request", TestCrawlDetailPaginationRejectsInvalidQuery)
	pool := newReportingTestPool(t)
	reader, err := NewPostgresReader(pool, PostgresConfig{MaxPendingProbes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.CrawlPage(context.Background(), 1, 0, 0); !errors.Is(err, errInvalidLimit) {
		t.Fatalf("invalid page size error=%v", err)
	}
	if _, _, err := reader.CrawlPage(context.Background(), 1, 1, -1); !errors.Is(err, errInvalidCursor) {
		t.Fatalf("invalid cursor error=%v", err)
	}
}
