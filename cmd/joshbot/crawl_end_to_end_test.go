package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
)

var errUnexpectedEndToEndCrawlPage = errors.New(
	"unexpected end-to-end crawl page",
)

type endToEndCrawlPage struct {
	body string
	err  error
}

type endToEndCrawlGetter struct {
	pages    map[string]endToEndCrawlPage
	requests []string
}

func (getter *endToEndCrawlGetter) Get(
	ctx context.Context,
	pageURL *url.URL,
) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	canonical := pageURL.String()
	getter.requests = append(
		getter.requests,
		canonical,
	)

	page, found := getter.pages[canonical]
	if !found {
		return nil, errUnexpectedEndToEndCrawlPage
	}

	if page.err != nil {
		return nil, page.err
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{
				"text/html; charset=utf-8",
			},
		},
		Body: io.NopCloser(
			strings.NewReader(page.body),
		),
	}, nil
}

func TestCuratedSeedCrawlToPublicRegistryEndToEnd(
	t *testing.T,
) {
	ctx := context.Background()
	operations, pool := newCLIIntegrationEnvironment(t)

	if err := operations.migrate(ctx); err != nil {
		t.Fatalf(
			"migrate() error = %v, want nil",
			err,
		)
	}

	seed := mustEndToEndCrawlOrigin(
		t,
		"https://DIRECTORY.example/start",
	)
	absentCandidate := mustEndToEndCrawlOrigin(
		t,
		"https://absent.example/path",
	)
	validCandidate := mustEndToEndCrawlOrigin(
		t,
		"https://valid.example/profile",
	)

	discoveryStore, err := store.NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	if err := discoveryStore.AddCrawlSeed(
		ctx,
		seed,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	getter := &endToEndCrawlGetter{
		pages: map[string]endToEndCrawlPage{
			"https://directory.example/": {
				body: `
					<!doctype html>
					<html>
						<body>
							<a href="/about">About</a>
							<a href="/private">Private</a>
							<a href="https://absent.example/path">
								Absent candidate
							</a>
						</body>
					</html>
				`,
			},
			"https://directory.example/about": {
				body: `
					<!doctype html>
					<html>
						<body>
							<a href="/team">Team</a>
							<a href="/">Home</a>
							<a href="https://valid.example/profile">
								Valid candidate
							</a>
						</body>
					</html>
				`,
			},
			"https://directory.example/private": {
				err: robots.ErrDisallowed,
			},
			"https://directory.example/team": {
				body: `
					<!doctype html>
					<html>
						<body>
							<a href="/about">About again</a>
							<a href="/">Home again</a>
							<a href="https://valid.example/another-path">
								Duplicate candidate origin
							</a>
						</body>
					</html>
				`,
			},
		},
	}

	crawler, err := discovery.NewMultiPageCrawler(
		getter,
		runtimeCandidateSink{
			store: discoveryStore,
		},
		discovery.CrawlConfig{
			MaxDepth:      4,
			MaxPages:      32,
			MaxPageBytes:  1024 * 1024,
			RequestDelay:  0,
			RedirectLimit: 5,
			PageTimeout:   time.Second,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewMultiPageCrawler() error = %v, want nil",
			err,
		)
	}

	runner, err := discovery.NewCrawlRunner(
		discoveryStore,
		crawler,
		discovery.CrawlRunnerConfig{
			DiscoveryInterval: 7 * 24 * time.Hour,
			PollInterval:      time.Second,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	report, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v, want nil",
			err,
		)
	}

	wantReport := discovery.CrawlReport{
		Worked:               true,
		Source:               seed,
		PagesAttempted:       4,
		PagesParsed:          3,
		CandidatesDiscovered: 2,
		BudgetExhausted:      false,
	}
	if report != wantReport {
		t.Errorf(
			"crawl report = %#v, want %#v",
			report,
			wantReport,
		)
	}

	wantRequests := []string{
		"https://directory.example/",
		"https://directory.example/about",
		"https://directory.example/private",
		"https://directory.example/team",
	}
	if !slices.Equal(
		getter.requests,
		wantRequests,
	) {
		t.Errorf(
			"crawl requests = %#v, want %#v",
			getter.requests,
			wantRequests,
		)
	}

	var (
		seeded          bool
		lastAttemptedAt *time.Time
		candidateCount  int
		edgeCount       int
		probeCount      int
		seedOriginCount int
		seedQueueCount  int
	)

	err = pool.QueryRow(
		ctx,
		`
			SELECT
				seeded,
				last_attempted_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		seed.String(),
	).Scan(
		&seeded,
		&lastAttemptedAt,
	)
	if err != nil {
		t.Fatalf(
			"read seed state: %v",
			err,
		)
	}

	if !seeded {
		t.Error("seeded = false, want true")
	}

	if lastAttemptedAt == nil {
		t.Error(
			"last_attempted_at = nil, want crawl attempt time",
		)
	}

	err = pool.QueryRow(
		ctx,
		`
			SELECT
				(
					SELECT count(*)
					FROM discovery_candidates
				),
				(
					SELECT count(*)
					FROM discovery_edges
					WHERE source_origin = $1
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE mode = 'probe'
				),
				(
					SELECT count(*)
					FROM origins
					WHERE origin = $1
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = $1
				)
		`,
		seed.String(),
	).Scan(
		&candidateCount,
		&edgeCount,
		&probeCount,
		&seedOriginCount,
		&seedQueueCount,
	)
	if err != nil {
		t.Fatalf(
			"read private crawl state: %v",
			err,
		)
	}

	if candidateCount != 2 {
		t.Errorf(
			"candidate count = %d, want 2",
			candidateCount,
		)
	}

	if edgeCount != 2 {
		t.Errorf(
			"edge count = %d, want 2",
			edgeCount,
		)
	}

	if probeCount != 2 {
		t.Errorf(
			"probe count = %d, want 2",
			probeCount,
		)
	}

	if seedOriginCount != 0 {
		t.Errorf(
			"seed origin count = %d, want 0",
			seedOriginCount,
		)
	}

	if seedQueueCount != 0 {
		t.Errorf(
			"seed queue count = %d, want 0",
			seedQueueCount,
		)
	}

	databaseStore := store.New(pool)
	verified, err := databaseStore.VerifiedOrigins(ctx)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() before probes error = %v, want nil",
			err,
		)
	}

	if len(verified) != 0 {
		t.Errorf(
			"verified origins before probes = %#v, want empty",
			verified,
		)
	}

	files, err := publicdata.Build(verified)
	if err != nil {
		t.Fatalf(
			"Build() before probes error = %v, want nil",
			err,
		)
	}

	if len(files) != 1 {
		t.Errorf(
			"public files before probes = %d, want 1",
			len(files),
		)
	}

	assertEndToEndPublicDataExcludes(
		t,
		files,
		seed.String(),
		absentCandidate.String(),
		validCandidate.String(),
	)

	queue, err := store.NewQueue(
		pool,
		store.QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewQueue() error = %v, want nil",
			err,
		)
	}

	probeResults := []declaration.Result{
		{
			Outcome: declaration.OutcomeAbsent,
			Origin:  absentCandidate,
		},
		{
			Outcome: declaration.OutcomeValid,
			Origin:  validCandidate,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	}

	for index, result := range probeResults {
		lease, found, claimErr := queue.Claim(
			ctx,
			"end-to-end-worker",
		)
		if claimErr != nil {
			t.Fatalf(
				"Claim(%d) error = %v, want nil",
				index,
				claimErr,
			)
		}

		if !found {
			t.Fatalf(
				"Claim(%d) found = false, want true",
				index,
			)
		}

		if lease.Origin != result.Origin {
			t.Fatalf(
				"Claim(%d) origin = %q, want %q",
				index,
				lease.Origin.String(),
				result.Origin.String(),
			)
		}

		if err := queue.CompleteVerification(
			ctx,
			lease,
			result,
			24*time.Hour,
		); err != nil {
			t.Fatalf(
				"CompleteVerification(%d) error = %v, want nil",
				index,
				err,
			)
		}
	}

	var (
		validRecurringCount int
		absentQueueCount    int
		remainingProbeCount int
		observationCount    int
	)

	err = pool.QueryRow(
		ctx,
		`
			SELECT
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = $1
						AND mode = 'recurring'
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = $2
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE mode = 'probe'
				),
				(
					SELECT count(*)
					FROM verification_observations
					WHERE origin IN ($1, $2)
				)
		`,
		validCandidate.String(),
		absentCandidate.String(),
	).Scan(
		&validRecurringCount,
		&absentQueueCount,
		&remainingProbeCount,
		&observationCount,
	)
	if err != nil {
		t.Fatalf(
			"read completed probe state: %v",
			err,
		)
	}

	if validRecurringCount != 1 {
		t.Errorf(
			"valid recurring count = %d, want 1",
			validRecurringCount,
		)
	}

	if absentQueueCount != 0 {
		t.Errorf(
			"absent queue count = %d, want 0",
			absentQueueCount,
		)
	}

	if remainingProbeCount != 0 {
		t.Errorf(
			"remaining probe count = %d, want 0",
			remainingProbeCount,
		)
	}

	if observationCount != 2 {
		t.Errorf(
			"candidate observation count = %d, want 2",
			observationCount,
		)
	}

	verified, err = databaseStore.VerifiedOrigins(ctx)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() after probes error = %v, want nil",
			err,
		)
	}

	if len(verified) != 1 {
		t.Fatalf(
			"verified origins after probes = %#v, want one",
			verified,
		)
	}

	if verified[0].Origin != validCandidate {
		t.Errorf(
			"verified origin = %q, want %q",
			verified[0].Origin.String(),
			validCandidate.String(),
		)
	}

	if verified[0].Declaration !=
		(declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		}) {
		t.Errorf(
			"verified declaration = %#v, want affirmed version 1",
			verified[0].Declaration,
		)
	}

	files, err = publicdata.Build(verified)
	if err != nil {
		t.Fatalf(
			"Build() after probes error = %v, want nil",
			err,
		)
	}

	if len(files) != 2 {
		t.Errorf(
			"public files after probes = %d, want 2",
			len(files),
		)
	}

	publicText := endToEndPublicText(files)
	if !strings.Contains(
		publicText,
		validCandidate.String(),
	) {
		t.Errorf(
			"public registry does not contain valid candidate %q",
			validCandidate.String(),
		)
	}

	for _, privateOrigin := range []string{
		seed.String(),
		absentCandidate.String(),
	} {
		if strings.Contains(
			publicText,
			privateOrigin,
		) {
			t.Errorf(
				"public registry contains private origin %q",
				privateOrigin,
			)
		}
	}
}

func mustEndToEndCrawlOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"Parse(%q) error = %v, want nil",
			raw,
			err,
		)
	}

	return parsed
}

func assertEndToEndPublicDataExcludes(
	t *testing.T,
	files []publicdata.File,
	origins ...string,
) {
	t.Helper()

	publicText := endToEndPublicText(files)
	for _, privateOrigin := range origins {
		if strings.Contains(
			publicText,
			privateOrigin,
		) {
			t.Errorf(
				"public registry contains private origin %q",
				privateOrigin,
			)
		}
	}
}

func endToEndPublicText(
	files []publicdata.File,
) string {
	var combined strings.Builder

	for _, file := range files {
		combined.WriteString(file.Path)
		combined.WriteByte('\n')
		combined.Write(file.Data)
		combined.WriteByte('\n')
	}

	return combined.String()
}
