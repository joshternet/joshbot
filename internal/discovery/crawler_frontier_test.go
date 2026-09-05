package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
)

var (
	errFrontierFetch = errors.New(
		"test frontier fetch failure",
	)
	errFrontierStore = errors.New(
		"test frontier candidate-store failure",
	)
)

func TestMultiPageCrawlerHonorsDepth(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)

	tests := []struct {
		name           string
		maxDepth       int
		wantCalls      []string
		wantAttempted  int
		wantParsed     int
		wantCandidates int
		wantExhausted  bool
	}{
		{
			name:          "homepage only",
			maxDepth:      0,
			wantCalls:     []string{"https://example.com/"},
			wantAttempted: 1,
			wantParsed:    1,
			wantExhausted: true,
		},
		{
			name:     "direct links",
			maxDepth: 1,
			wantCalls: []string{
				"https://example.com/",
				"https://example.com/a",
			},
			wantAttempted: 2,
			wantParsed:    2,
			wantExhausted: true,
		},
		{
			name:     "second level",
			maxDepth: 2,
			wantCalls: []string{
				"https://example.com/",
				"https://example.com/a",
				"https://example.com/b",
			},
			wantAttempted:  3,
			wantParsed:     3,
			wantCandidates: 1,
			wantExhausted:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := newFrontierGetter(
				map[string][]frontierStep{
					"https://example.com/": {
						frontierHTML(
							`<a href="/a">a</a>`,
						),
					},
					"https://example.com/a": {
						frontierHTML(
							`<a href="/b">b</a>`,
						),
					},
					"https://example.com/b": {
						frontierHTML(
							`<a href="https://external.example/path">external</a>`,
						),
					},
				},
			)
			sink := &frontierCandidateSink{}
			config := frontierCrawlConfig()
			config.MaxDepth = test.maxDepth

			crawler := mustNewMultiPageCrawler(
				t,
				getter,
				sink,
				config,
				&frontierWaiter{},
				contextTimeoutFactory{},
			)

			result, err := crawler.Crawl(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf(
					"Crawl() error = %v, want nil",
					err,
				)
			}

			want := CrawlResult{
				Source:               source,
				PagesAttempted:       test.wantAttempted,
				PagesParsed:          test.wantParsed,
				CandidatesDiscovered: test.wantCandidates,
				BudgetExhausted:      test.wantExhausted,
			}
			if result != want {
				t.Errorf(
					"Crawl() = %#v, want %#v",
					result,
					want,
				)
			}

			if !reflect.DeepEqual(
				getter.calls,
				test.wantCalls,
			) {
				t.Errorf(
					"fetch calls = %#v, want %#v",
					getter.calls,
					test.wantCalls,
				)
			}
		})
	}
}

func TestMultiPageCrawlerUsesBreadthFirstDocumentOrder(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/a">a</a>
					<a href="/b">b</a>
				`),
			},
			"https://example.com/a": {
				frontierHTML(
					`<a href="/deep">deep</a>`,
				),
			},
			"https://example.com/b": {
				frontierHTML(
					`<a href="https://external.example/path">external</a>`,
				),
			},
			"https://example.com/deep": {
				frontierHTML("<html></html>"),
			},
		},
	)
	sink := &frontierCandidateSink{}
	config := frontierCrawlConfig()
	config.MaxDepth = 2

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		config,
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/deep",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	want := CrawlResult{
		Source:               source,
		PagesAttempted:       4,
		PagesParsed:          4,
		CandidatesDiscovered: 1,
	}
	if result != want {
		t.Errorf(
			"Crawl() = %#v, want %#v",
			result,
			want,
		)
	}
}

func TestMultiPageCrawlerCollapsesCyclesAndDuplicates(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/a#one">a one</a>
					<a href="/a#two">a two</a>
					<a href="/">root</a>
				`),
			},
			"https://example.com/a": {
				frontierHTML(`
					<a href="/b">b</a>
					<a href="/">root</a>
				`),
			},
			"https://example.com/b": {
				frontierHTML(`
					<a href="/a">a</a>
					<a href="/b#fragment">b</a>
				`),
			},
		},
	)

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		&frontierCandidateSink{},
		frontierCrawlConfig(),
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/a",
		"https://example.com/b",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	if result.PagesAttempted != 3 ||
		result.PagesParsed != 3 ||
		result.BudgetExhausted {
		t.Errorf(
			"Crawl() = %#v",
			result,
		)
	}
}

func TestMultiPageCrawlerObeysPageBudgetExactly(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/a">a</a>
					<a href="/b">b</a>
					<a href="/c">c</a>
				`),
			},
			"https://example.com/a": {
				frontierHTML("<html></html>"),
			},
		},
	)
	waiter := &frontierWaiter{}
	config := frontierCrawlConfig()
	config.MaxPages = 2

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		&frontierCandidateSink{},
		config,
		waiter,
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/a",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	if result.PagesAttempted != 2 {
		t.Errorf(
			"PagesAttempted = %d, want 2",
			result.PagesAttempted,
		)
	}

	if !result.BudgetExhausted {
		t.Error(
			"BudgetExhausted = false, want true",
		)
	}
}

func TestMultiPageCrawlerFollowsAndDeduplicatesSameOriginRedirect(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/alias">alias</a>
					<a href="/final#queued">final</a>
				`),
			},
			"https://example.com/alias": {
				frontierRedirect(
					"/final#redirected",
				),
			},
			"https://example.com/final": {
				frontierHTML(
					`<a href="https://external.example/path">external</a>`,
				),
			},
		},
	)
	sink := &frontierCandidateSink{}

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		frontierCrawlConfig(),
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/alias",
		"https://example.com/final",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	want := CrawlResult{
		Source:               source,
		PagesAttempted:       2,
		PagesParsed:          2,
		CandidatesDiscovered: 1,
	}
	if result != want {
		t.Errorf(
			"Crawl() = %#v, want %#v",
			result,
			want,
		)
	}

	wantCandidates := []string{
		"https://external.example",
	}
	if got := frontierSinkOrigins(
		sink.calls,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"stored candidates = %#v, want %#v",
			got,
			wantCandidates,
		)
	}
}

func TestMultiPageCrawlerRecordsCrossOriginRedirectAndContinues(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/leave">leave</a>
					<a href="/ok">ok</a>
				`),
			},
			"https://example.com/leave": {
				frontierRedirect(
					"https://redirect.example/path?q=1#fragment",
				),
			},
			"https://example.com/ok": {
				frontierHTML(
					`<a href="https://linked.example/path">linked</a>`,
				),
			},
		},
	)
	sink := &frontierCandidateSink{}

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		frontierCrawlConfig(),
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/leave",
		"https://example.com/ok",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	wantCandidates := []frontierStoredCandidate{
		{
			source: source,
			candidate: Candidate{
				Origin: mustDiscoveryOrigin(
					t,
					"https://redirect.example",
				),
				Kind: KindRedirect,
			},
		},
		{
			source: source,
			candidate: Candidate{
				Origin: mustDiscoveryOrigin(
					t,
					"https://linked.example",
				),
				Kind: KindLink,
			},
		},
	}
	if got := frontierStoredCandidates(
		sink.calls,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"stored candidates = %#v, want %#v",
			got,
			wantCandidates,
		)
	}

	if result.CandidatesDiscovered != 2 ||
		result.PagesAttempted != 3 ||
		result.PagesParsed != 2 {
		t.Errorf(
			"Crawl() = %#v",
			result,
		)
	}
}

func TestMultiPageCrawlerBoundsRedirectsAndContinues(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/loop">loop</a>
					<a href="/ok">ok</a>
				`),
			},
			"https://example.com/loop": {
				frontierRedirect("/loop-two"),
			},
			"https://example.com/loop-two": {
				frontierRedirect("/loop-three"),
			},
			"https://example.com/ok": {
				frontierHTML(
					`<a href="https://external.example/">external</a>`,
				),
			},
		},
	)
	sink := &frontierCandidateSink{}
	config := frontierCrawlConfig()
	config.RedirectLimit = 1

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		config,
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/loop",
		"https://example.com/loop-two",
		"https://example.com/ok",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	if result.PagesAttempted != 3 ||
		result.PagesParsed != 2 ||
		result.CandidatesDiscovered != 1 {
		t.Errorf(
			"Crawl() = %#v",
			result,
		)
	}
}

func TestMultiPageCrawlerSkipsRemotePageFailuresAndContinues(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/denied">denied</a>
					<a href="/failed">failed</a>
					<a href="/ok">ok</a>
				`),
			},
			"https://example.com/denied": {
				frontierFailure(
					robots.ErrDisallowed,
				),
			},
			"https://example.com/failed": {
				frontierFailure(
					errFrontierFetch,
				),
			},
			"https://example.com/ok": {
				frontierHTML(
					`<a href="https://external.example/path">external</a>`,
				),
			},
		},
	)
	sink := &frontierCandidateSink{}

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		frontierCrawlConfig(),
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/denied",
		"https://example.com/failed",
		"https://example.com/ok",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	if result.PagesAttempted != 4 {
		t.Errorf(
			"PagesAttempted = %d, want 4",
			result.PagesAttempted,
		)
	}

	if result.PagesParsed != 2 {
		t.Errorf(
			"PagesParsed = %d, want 2",
			result.PagesParsed,
		)
	}

	if result.CandidatesDiscovered != 1 {
		t.Errorf(
			"CandidatesDiscovered = %d, want 1",
			result.CandidatesDiscovered,
		)
	}
}

func TestMultiPageCrawlerSkipsUnsupportedAndOversizedPages(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	config := frontierCrawlConfig()
	config.MaxPageBytes = 256

	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/json">json</a>
					<a href="/large">large</a>
					<a href="/ok">ok</a>
				`),
			},
			"https://example.com/json": {
				{
					status:      http.StatusOK,
					contentType: "application/json",
					body: `{
						"link": "https://ignored.example/"
					}`,
				},
			},
			"https://example.com/large": {
				{
					status:      http.StatusOK,
					contentType: "text/html",
					body: strings.Repeat(
						"x",
						config.MaxPageBytes+1,
					),
				},
			},
			"https://example.com/ok": {
				frontierHTML(
					`<a href="https://external.example/path">external</a>`,
				),
			},
		},
	)
	sink := &frontierCandidateSink{}

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		config,
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	if result.PagesAttempted != 4 {
		t.Errorf(
			"PagesAttempted = %d, want 4",
			result.PagesAttempted,
		)
	}

	if result.PagesParsed != 2 {
		t.Errorf(
			"PagesParsed = %d, want 2",
			result.PagesParsed,
		)
	}

	if result.CandidatesDiscovered != 1 {
		t.Errorf(
			"CandidatesDiscovered = %d, want 1",
			result.CandidatesDiscovered,
		)
	}

	wantCandidates := []string{
		"https://external.example",
	}
	if got := frontierSinkOrigins(
		sink.calls,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"stored candidates = %#v, want %#v",
			got,
			wantCandidates,
		)
	}
}

func TestMultiPageCrawlerDeduplicatesCandidatesAcrossPages(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="https://shared.example/root">shared</a>
					<a href="https://a.example/root">a</a>
					<a href="/one">one</a>
					<a href="/two">two</a>
				`),
			},
			"https://example.com/one": {
				frontierHTML(`
					<a href="https://shared.example/one">shared</a>
					<a href="https://b.example/one">b</a>
				`),
			},
			"https://example.com/two": {
				frontierHTML(`
					<a href="https://a.example/two">a</a>
					<a href="https://c.example/two">c</a>
				`),
			},
		},
	)
	sink := &frontierCandidateSink{}
	config := frontierCrawlConfig()
	config.MaxDepth = 1

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		config,
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCandidates := []string{
		"https://a.example",
		"https://shared.example",
		"https://b.example",
		"https://c.example",
	}
	if got := frontierSinkOrigins(
		sink.calls,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"stored candidates = %#v, want %#v",
			got,
			wantCandidates,
		)
	}

	if result.CandidatesDiscovered != 4 {
		t.Errorf(
			"CandidatesDiscovered = %d, want 4",
			result.CandidatesDiscovered,
		)
	}

	if len(sink.calls) != 3 {
		t.Errorf(
			"candidate sink calls = %d, want 3",
			len(sink.calls),
		)
	}
}

func TestMultiPageCrawlerStreamsEveryLargeDirectoryCandidate(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://directory.example",
	)

	var document strings.Builder
	document.WriteString("<!doctype html><html><body>")

	for index := largeDirectoryCandidateCount - 1; index >= 0; index-- {
		fmt.Fprintf(
			&document,
			`<a href="https://candidate-%04d.example/path/%d">candidate</a>`,
			index,
			index,
		)
	}

	document.WriteString("</body></html>")
	body := document.String()

	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://directory.example/": {
				frontierHTML(body),
			},
		},
	)
	sink := &frontierCandidateSink{}
	config := frontierCrawlConfig()
	config.MaxDepth = 0
	config.MaxPages = 1
	config.MaxPageBytes = len(body) + 1

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		config,
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	if result.CandidatesDiscovered !=
		largeDirectoryCandidateCount {
		t.Errorf(
			"CandidatesDiscovered = %d, want %d",
			result.CandidatesDiscovered,
			largeDirectoryCandidateCount,
		)
	}

	if len(sink.calls) != 1 {
		t.Fatalf(
			"candidate sink calls = %d, want 1",
			len(sink.calls),
		)
	}

	if len(sink.calls[0].candidates) !=
		largeDirectoryCandidateCount {
		t.Fatalf(
			"stored candidate count = %d, want %d",
			len(sink.calls[0].candidates),
			largeDirectoryCandidateCount,
		)
	}

	for index, candidate := range sink.calls[0].candidates {
		wantOrigin := fmt.Sprintf(
			"https://candidate-%04d.example",
			index,
		)

		if candidate.Origin.String() != wantOrigin {
			t.Errorf(
				"candidate %d = %q, want %q",
				index,
				candidate.Origin,
				wantOrigin,
			)
		}
	}
}

func TestMultiPageCrawlerPacesFrontierRequests(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/a">a</a>
					<a href="/b">b</a>
				`),
			},
			"https://example.com/a": {
				frontierHTML("<html></html>"),
			},
			"https://example.com/b": {
				frontierHTML("<html></html>"),
			},
		},
	)
	waiter := &frontierWaiter{}
	config := frontierCrawlConfig()
	config.MaxDepth = 1
	config.RequestDelay = 250 * time.Millisecond

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		&frontierCandidateSink{},
		config,
		waiter,
		contextTimeoutFactory{},
	)

	_, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantDurations := []time.Duration{
		config.RequestDelay,
		config.RequestDelay,
	}
	if !reflect.DeepEqual(
		waiter.durations,
		wantDurations,
	) {
		t.Errorf(
			"wait durations = %#v, want %#v",
			waiter.durations,
			wantDurations,
		)
	}

	if len(getter.calls) != 3 {
		t.Errorf(
			"fetch calls = %d, want 3",
			len(getter.calls),
		)
	}
}

func TestMultiPageCrawlerContinuesAfterPageTimeout(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="/timeout">timeout</a>
					<a href="/ok">ok</a>
				`),
			},
			"https://example.com/timeout": {
				frontierHTML("<html></html>"),
			},
			"https://example.com/ok": {
				frontierHTML(
					`<a href="https://external.example/path">external</a>`,
				),
			},
		},
	)
	timeoutFactory := &frontierTimeoutFactory{
		cancelCalls: map[int]bool{
			2: true,
		},
	}
	sink := &frontierCandidateSink{}

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		frontierCrawlConfig(),
		&frontierWaiter{},
		timeoutFactory,
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
		"https://example.com/timeout",
		"https://example.com/ok",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	if result.PagesAttempted != 3 ||
		result.PagesParsed != 2 ||
		result.CandidatesDiscovered != 1 {
		t.Errorf(
			"Crawl() = %#v",
			result,
		)
	}

	if len(timeoutFactory.durations) != 3 {
		t.Errorf(
			"timeout calls = %d, want 3",
			len(timeoutFactory.durations),
		)
	}
}

func TestMultiPageCrawlerPreservesParentCancellation(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)

	t.Run("during fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		getter := frontierGetterFunc(
			func(
				requestContext context.Context,
				_ *url.URL,
			) (*http.Response, error) {
				cancel()
				return nil, requestContext.Err()
			},
		)

		crawler := mustNewMultiPageCrawler(
			t,
			getter,
			&frontierCandidateSink{},
			frontierCrawlConfig(),
			&frontierWaiter{},
			contextTimeoutFactory{},
		)

		_, err := crawler.Crawl(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"Crawl() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("during pacing wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		getter := newFrontierGetter(
			map[string][]frontierStep{
				"https://example.com/": {
					frontierHTML(
						`<a href="/next">next</a>`,
					),
				},
			},
		)
		waiter := &frontierWaiter{
			wait: func(
				waitContext context.Context,
				_ time.Duration,
			) error {
				cancel()
				return waitContext.Err()
			},
		}
		config := frontierCrawlConfig()
		config.RequestDelay = time.Second

		crawler := mustNewMultiPageCrawler(
			t,
			getter,
			&frontierCandidateSink{},
			config,
			waiter,
			contextTimeoutFactory{},
		)

		_, err := crawler.Crawl(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"Crawl() error = %v, want context.Canceled",
				err,
			)
		}

		wantCalls := []string{
			"https://example.com/",
		}
		if !reflect.DeepEqual(
			getter.calls,
			wantCalls,
		) {
			t.Errorf(
				"fetch calls = %#v, want %#v",
				getter.calls,
				wantCalls,
			)
		}
	})
}

func TestMultiPageCrawlerAbortsOnCandidateStoreFailure(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierHTML(`
					<a href="https://external.example/path">external</a>
					<a href="/next">next</a>
				`),
			},
		},
	)
	sink := &frontierCandidateSink{
		err:      errFrontierStore,
		failCall: 1,
	}

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		sink,
		frontierCrawlConfig(),
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	_, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if !errors.Is(err, errFrontierStore) {
		t.Errorf(
			"Crawl() error = %v, want errFrontierStore",
			err,
		)
	}

	wantCalls := []string{
		"https://example.com/",
	}
	if !reflect.DeepEqual(
		getter.calls,
		wantCalls,
	) {
		t.Errorf(
			"fetch calls = %#v, want %#v",
			getter.calls,
			wantCalls,
		)
	}

	if len(sink.calls) != 1 {
		t.Errorf(
			"candidate sink calls = %d, want 1",
			len(sink.calls),
		)
	}
}

func TestMultiPageCrawlerRootFailureEndsAttempt(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	getter := newFrontierGetter(
		map[string][]frontierStep{
			"https://example.com/": {
				frontierFailure(errFrontierFetch),
			},
		},
	)

	crawler := mustNewMultiPageCrawler(
		t,
		getter,
		&frontierCandidateSink{},
		frontierCrawlConfig(),
		&frontierWaiter{},
		contextTimeoutFactory{},
	)

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v, want nil",
			err,
		)
	}

	want := CrawlResult{
		Source:         source,
		PagesAttempted: 1,
	}
	if result != want {
		t.Errorf(
			"Crawl() = %#v, want %#v",
			result,
			want,
		)
	}
}

func TestMultiPageCrawlerValidatesConfigurationAndDependencies(
	t *testing.T,
) {
	getter := newFrontierGetter(
		map[string][]frontierStep{},
	)
	sink := &frontierCandidateSink{}
	waiter := &frontierWaiter{}
	timeoutFactory := contextTimeoutFactory{}
	validConfig := frontierCrawlConfig()

	if crawler, err := NewMultiPageCrawler(
		nil,
		sink,
		validConfig,
	); err == nil || crawler != nil {
		t.Errorf(
			"NewMultiPageCrawler(nil getter) = %#v, %v",
			crawler,
			err,
		)
	}

	if crawler, err := NewMultiPageCrawler(
		getter,
		nil,
		validConfig,
	); err == nil || crawler != nil {
		t.Errorf(
			"NewMultiPageCrawler(nil sink) = %#v, %v",
			crawler,
			err,
		)
	}

	invalidConfigs := []CrawlConfig{
		{
			MaxDepth:      -1,
			MaxPages:      1,
			MaxPageBytes:  1,
			RedirectLimit: 1,
			PageTimeout:   time.Second,
		},
		{
			MaxDepth:      0,
			MaxPages:      0,
			MaxPageBytes:  1,
			RedirectLimit: 1,
			PageTimeout:   time.Second,
		},
		{
			MaxDepth:      0,
			MaxPages:      1,
			MaxPageBytes:  0,
			RedirectLimit: 1,
			PageTimeout:   time.Second,
		},
		{
			MaxDepth:      0,
			MaxPages:      1,
			MaxPageBytes:  1,
			RequestDelay:  -time.Nanosecond,
			RedirectLimit: 1,
			PageTimeout:   time.Second,
		},
		{
			MaxDepth:      0,
			MaxPages:      1,
			MaxPageBytes:  1,
			RedirectLimit: 0,
			PageTimeout:   time.Second,
		},
		{
			MaxDepth:      0,
			MaxPages:      1,
			MaxPageBytes:  1,
			RedirectLimit: 1,
			PageTimeout:   0,
		},
	}

	for index, config := range invalidConfigs {
		crawler, err := NewMultiPageCrawler(
			getter,
			sink,
			config,
		)
		if err == nil || crawler != nil {
			t.Errorf(
				"invalid config %d returned %#v, %v",
				index,
				crawler,
				err,
			)
		}
	}

	if crawler, err := newMultiPageCrawler(
		getter,
		sink,
		validConfig,
		nil,
		timeoutFactory,
	); err == nil || crawler != nil {
		t.Errorf(
			"newMultiPageCrawler(nil waiter) = %#v, %v",
			crawler,
			err,
		)
	}

	if crawler, err := newMultiPageCrawler(
		getter,
		sink,
		validConfig,
		waiter,
		nil,
	); err == nil || crawler != nil {
		t.Errorf(
			"newMultiPageCrawler(nil timeout factory) = %#v, %v",
			crawler,
			err,
		)
	}

	crawler, err := NewMultiPageCrawler(
		getter,
		sink,
		validConfig,
	)
	if err != nil {
		t.Fatalf(
			"NewMultiPageCrawler() error = %v, want nil",
			err,
		)
	}

	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)

	var nilCrawler *MultiPageCrawler
	if _, err := nilCrawler.Crawl(
		context.Background(),
		source,
	); err == nil {
		t.Error(
			"nil crawler error = nil, want non-nil",
		)
	}

	if _, err := crawler.Crawl(
		nil,
		source,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"nil context error = %v, want errInvalidContext",
			err,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := crawler.Crawl(
		canceledContext,
		source,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"canceled context error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := crawler.Crawl(
		context.Background(),
		origin.Origin{},
	); !errors.Is(err, errInvalidSource) {
		t.Errorf(
			"zero source error = %v, want errInvalidSource",
			err,
		)
	}

	broken := *crawler
	broken.getter = nil
	if _, err := broken.Crawl(
		context.Background(),
		source,
	); err == nil {
		t.Error(
			"stored nil getter error = nil, want non-nil",
		)
	}

	broken = *crawler
	broken.sink = nil
	if _, err := broken.Crawl(
		context.Background(),
		source,
	); err == nil {
		t.Error(
			"stored nil sink error = nil, want non-nil",
		)
	}

	broken = *crawler
	broken.waiter = nil
	if _, err := broken.Crawl(
		context.Background(),
		source,
	); err == nil {
		t.Error(
			"stored nil waiter error = nil, want non-nil",
		)
	}

	broken = *crawler
	broken.timeoutFactory = nil
	if _, err := broken.Crawl(
		context.Background(),
		source,
	); err == nil {
		t.Error(
			"stored nil timeout factory error = nil, want non-nil",
		)
	}

	broken = *crawler
	broken.config.MaxPages = 0
	if _, err := broken.Crawl(
		context.Background(),
		source,
	); err == nil {
		t.Error(
			"stored invalid config error = nil, want non-nil",
		)
	}
}

func TestCrawlResultContainsOnlyEphemeralStatistics(
	t *testing.T,
) {
	resultType := reflect.TypeOf(CrawlResult{})

	wantFields := map[string]reflect.Type{
		"Source": reflect.TypeOf(
			origin.Origin{},
		),
		"PagesAttempted": reflect.TypeOf(
			int(0),
		),
		"PagesParsed": reflect.TypeOf(
			int(0),
		),
		"CandidatesDiscovered": reflect.TypeOf(
			int(0),
		),
		"BudgetExhausted": reflect.TypeOf(
			false,
		),
	}

	if resultType.NumField() != len(wantFields) {
		t.Fatalf(
			"CrawlResult field count = %d, want %d",
			resultType.NumField(),
			len(wantFields),
		)
	}

	for fieldName, wantType := range wantFields {
		field, found := resultType.FieldByName(
			fieldName,
		)
		if !found {
			t.Errorf(
				"CrawlResult is missing field %q",
				fieldName,
			)
			continue
		}

		if field.Type != wantType {
			t.Errorf(
				"CrawlResult.%s type = %v, want %v",
				fieldName,
				field.Type,
				wantType,
			)
		}
	}
}

type frontierStep struct {
	status      int
	contentType string
	location    string
	body        string
	err         error
}

type frontierGetter struct {
	steps     map[string][]frontierStep
	positions map[string]int
	calls     []string
}

func newFrontierGetter(
	steps map[string][]frontierStep,
) *frontierGetter {
	return &frontierGetter{
		steps:     steps,
		positions: make(map[string]int),
	}
}

func (g *frontierGetter) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	canonical := target.String()
	g.calls = append(g.calls, canonical)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	steps, exists := g.steps[canonical]
	position := g.positions[canonical]

	if !exists || position >= len(steps) {
		return nil, fmt.Errorf(
			"%w: %s",
			errUnexpectedFetch,
			canonical,
		)
	}

	g.positions[canonical] = position + 1
	step := steps[position]

	if step.err != nil {
		return nil, step.err
	}

	status := step.status
	if status == 0 {
		status = http.StatusOK
	}

	header := make(http.Header)
	if step.contentType != "" {
		header.Set(
			"Content-Type",
			step.contentType,
		)
	}
	if step.location != "" {
		header.Set(
			"Location",
			step.location,
		)
	}

	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body: io.NopCloser(
			strings.NewReader(step.body),
		),
	}, nil
}

type frontierGetterFunc func(
	context.Context,
	*url.URL,
) (*http.Response, error)

func (f frontierGetterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return f(ctx, target)
}

func frontierHTML(
	body string,
) frontierStep {
	return frontierStep{
		status:      http.StatusOK,
		contentType: "text/html; charset=utf-8",
		body:        body,
	}
}

func frontierRedirect(
	location string,
) frontierStep {
	return frontierStep{
		status:   http.StatusFound,
		location: location,
	}
}

func frontierFailure(
	err error,
) frontierStep {
	return frontierStep{err: err}
}

type frontierCandidateCall struct {
	source     origin.Origin
	candidates []Candidate
}

type frontierCandidateSink struct {
	calls    []frontierCandidateCall
	err      error
	failCall int
}

func (s *frontierCandidateSink) RecordCandidates(
	_ context.Context,
	source origin.Origin,
	candidates []Candidate,
) error {
	s.calls = append(
		s.calls,
		frontierCandidateCall{
			source: source,
			candidates: append(
				[]Candidate(nil),
				candidates...,
			),
		},
	)

	if s.failCall > 0 &&
		len(s.calls) == s.failCall {
		return s.err
	}

	return nil
}

type frontierStoredCandidate struct {
	source    origin.Origin
	candidate Candidate
}

func frontierStoredCandidates(
	calls []frontierCandidateCall,
) []frontierStoredCandidate {
	var stored []frontierStoredCandidate

	for _, call := range calls {
		for _, candidate := range call.candidates {
			stored = append(
				stored,
				frontierStoredCandidate{
					source:    call.source,
					candidate: candidate,
				},
			)
		}
	}

	return stored
}

func frontierSinkOrigins(
	calls []frontierCandidateCall,
) []string {
	stored := frontierStoredCandidates(calls)
	origins := make(
		[]string,
		len(stored),
	)

	for index, candidate := range stored {
		origins[index] =
			candidate.candidate.Origin.String()
	}

	return origins
}

type frontierWaiter struct {
	durations []time.Duration
	wait      func(context.Context, time.Duration) error
}

func (w *frontierWaiter) Wait(
	ctx context.Context,
	duration time.Duration,
) error {
	w.durations = append(
		w.durations,
		duration,
	)

	if w.wait != nil {
		return w.wait(ctx, duration)
	}

	return nil
}

type frontierTimeoutFactory struct {
	calls       int
	durations   []time.Duration
	cancelCalls map[int]bool
}

func (f *frontierTimeoutFactory) WithTimeout(
	ctx context.Context,
	duration time.Duration,
) (context.Context, context.CancelFunc) {
	f.calls++
	f.durations = append(
		f.durations,
		duration,
	)

	pageContext, cancel := context.WithCancel(ctx)
	if f.cancelCalls[f.calls] {
		cancel()
	}

	return pageContext, cancel
}

func frontierCrawlConfig() CrawlConfig {
	return CrawlConfig{
		MaxDepth:      2,
		MaxPages:      16,
		MaxPageBytes:  MaxRawBody,
		RequestDelay:  0,
		RedirectLimit: 2,
		PageTimeout:   time.Second,
	}
}

func mustNewMultiPageCrawler(
	t *testing.T,
	getter Getter,
	sink CandidateSink,
	config CrawlConfig,
	waiter waitStrategy,
	timeoutFactory timeoutFactory,
) *MultiPageCrawler {
	t.Helper()

	crawler, err := newMultiPageCrawler(
		getter,
		sink,
		config,
		waiter,
		timeoutFactory,
	)
	if err != nil {
		t.Fatalf(
			"newMultiPageCrawler() error = %v, want nil",
			err,
		)
	}

	return crawler
}
