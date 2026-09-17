package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

func TestRunDispatchesCrawlSourceCommands(t *testing.T) {
	first := time.Date(2026, 9, 1, 2, 3, 4, 0, time.FixedZone("test", 3600))
	last := first.Add(time.Hour)
	listed := store.CrawlSource{
		Origin: mustCommandSeedOrigin(t, "https://example.com"),
		Seeded: true, AutomaticallyDiscovered: true, Verified: true,
		FirstDiscoveredAt: &first, LastDiscoveredAt: &last,
	}
	tests := []struct {
		args                 []string
		wantCall, wantOutput string
	}{
		{[]string{"source", "block", "https://EXAMPLE.com/path"}, "block", "source blocked https://example.com\n"},
		{[]string{"source", "allow", "https://EXAMPLE.com/path"}, "allow", "source allowed https://example.com\n"},
		{[]string{"source", "list"}, "list", "https://example.com\tcurated=true\tdiscovered=true\tverified=true\tblocked=false\tfirst_discovered=2026-09-01T01:03:04Z\tlast_discovered=2026-09-01T02:03:04Z\n"},
	}
	for _, test := range tests {
		t.Run(test.wantCall, func(t *testing.T) {
			operations := &fakeSourceOperations{sources: []store.CrawlSource{listed}}
			var stdout, stderr bytes.Buffer
			code := runWithOperations(context.Background(), test.args, &stdout, &stderr, operations)
			if code != exitSuccess || stdout.String() != test.wantOutput || stderr.Len() != 0 || operations.call != test.wantCall {
				t.Fatalf("code=%d stdout=%q stderr=%q call=%q", code, stdout.String(), stderr.String(), operations.call)
			}
		})
	}
}

func TestRunRejectsInvalidCrawlSourceCommands(t *testing.T) {
	for _, args := range [][]string{
		{"source"}, {"source", "block"}, {"source", "allow", "bad"},
		{"source", "block", "https://example.com", "extra"},
		{"source", "list", "extra"}, {"source", "unknown"},
	} {
		var stdout, stderr bytes.Buffer
		code := runWithOperations(context.Background(), args, &stdout, &stderr, &fakeSourceOperations{})
		if code != exitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "source") {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	code := runWithOperations(context.Background(), []string{"source", "list"}, &stdout, &stderr, &fakeCommandOperations{})
	if code != exitFailure || !strings.Contains(stderr.String(), errOperationsUnavailable.Error()) {
		t.Fatalf("unavailable code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunReturnsCrawlSourceFailures(t *testing.T) {
	operationErr := errors.New("source failed")
	for _, args := range [][]string{
		{"source", "block", "https://example.com"},
		{"source", "allow", "https://example.com"},
		{"source", "list"},
	} {
		var stdout, stderr bytes.Buffer
		code := runWithOperations(context.Background(), args, &stdout, &stderr, &fakeSourceOperations{err: operationErr})
		if code != exitFailure || stdout.Len() != 0 || !strings.Contains(stderr.String(), operationErr.Error()) {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestFormatCrawlSourceWithoutDiscoveryTimes(t *testing.T) {
	source := store.CrawlSource{Origin: mustCommandSeedOrigin(t, "http://example.com")}
	want := "http://example.com\tcurated=false\tdiscovered=false\tverified=false\tblocked=false\tfirst_discovered=-\tlast_discovered=-"
	if got := formatCrawlSource(source); got != want {
		t.Fatalf("formatCrawlSource()=%q want %q", got, want)
	}
}

func TestRuntimeCrawlSourceOperations(t *testing.T) {
	operations, state := newTestRuntimeOperations(t)
	stored := []store.CrawlSource{{Origin: mustCommandSeedOrigin(t, "https://example.com")}}
	fakeStore := &fakeRuntimeCrawlSourceStore{sources: stored}
	operations.newCrawlSourceStore = func(pool *pgxpool.Pool) (crawlSourceStore, error) {
		if pool != state.database.Pool() {
			t.Error("unexpected pool")
		}
		return fakeStore, nil
	}
	source := mustCommandSeedOrigin(t, "https://example.com")
	if err := operations.setCrawlBlocked(context.Background(), source, true); err != nil {
		t.Fatal(err)
	}
	if fakeStore.source != source || !fakeStore.blocked {
		t.Fatalf("block state = %#v %t", fakeStore.source, fakeStore.blocked)
	}
	sources, err := operations.crawlSources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].Origin != source {
		t.Fatalf("sources=%#v err=%v", sources, err)
	}
}

func TestRuntimeCrawlSourceFailures(t *testing.T) {
	want := errors.New("source store failure")
	source := mustCommandSeedOrigin(t, "https://example.com")
	for _, construction := range []bool{false, true} {
		operations, _ := newTestRuntimeOperations(t)
		operations.newCrawlSourceStore = func(*pgxpool.Pool) (crawlSourceStore, error) {
			if construction {
				return nil, want
			}
			return &fakeRuntimeCrawlSourceStore{err: want}, nil
		}
		if err := operations.setCrawlBlocked(context.Background(), source, true); !errors.Is(err, want) {
			t.Fatalf("set error=%v", err)
		}
		if _, err := operations.crawlSources(context.Background()); !errors.Is(err, want) {
			t.Fatalf("list error=%v", err)
		}
	}
}

func TestProductionRuntimeIncludesCrawlSourceStore(t *testing.T) {
	operations := newRuntimeOperations(testWriter{t: t})
	if operations.newCrawlSourceStore == nil {
		t.Fatal("crawl source factory is nil")
	}
	if sourceStore, err := newRuntimeCrawlSourceStore(nil); err == nil || sourceStore != nil {
		t.Fatalf("newRuntimeCrawlSourceStore(nil)=(%#v,%v)", sourceStore, err)
	}
}

type fakeSourceOperations struct {
	fakeCommandOperations
	sources []store.CrawlSource
	call    string
	err     error
}

type fakeRuntimeCrawlSourceStore struct {
	source  origin.Origin
	blocked bool
	sources []store.CrawlSource
	err     error
}

func (sourceStore *fakeRuntimeCrawlSourceStore) SetCrawlBlocked(_ context.Context, source origin.Origin, blocked bool) error {
	sourceStore.source, sourceStore.blocked = source, blocked
	return sourceStore.err
}

func (sourceStore *fakeRuntimeCrawlSourceStore) CrawlSources(context.Context) ([]store.CrawlSource, error) {
	return sourceStore.sources, sourceStore.err
}

type testWriter struct{ t *testing.T }

func (writer testWriter) Write(value []byte) (int, error) {
	writer.t.Log(string(value))
	return len(value), nil
}

func (operations *fakeSourceOperations) setCrawlBlocked(_ context.Context, _ origin.Origin, blocked bool) error {
	if blocked {
		operations.call = "block"
	} else {
		operations.call = "allow"
	}
	return operations.err
}

func (operations *fakeSourceOperations) crawlSources(context.Context) ([]store.CrawlSource, error) {
	operations.call = "list"
	return operations.sources, operations.err
}
