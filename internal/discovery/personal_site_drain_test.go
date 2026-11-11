package discovery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

type drainingSourceCrawler struct {
	fakeSourceCrawler
	drainErr error
	drained  bool
}

func (crawler *drainingSourceCrawler) DrainPersonalSiteQualifications(
	context.Context,
) error {
	crawler.drained = true
	return crawler.drainErr
}

type drainingSink struct {
	drained bool
	err     error
}

func (sink *drainingSink) RecordCandidates(
	context.Context,
	origin.Origin,
	[]Candidate,
) error {
	return nil
}

func (sink *drainingSink) DrainPersonalSiteQualifications(
	context.Context,
) error {
	sink.drained = true
	return sink.err
}

type nonDrainingSink struct{}

func (nonDrainingSink) RecordCandidates(
	context.Context,
	origin.Origin,
	[]Candidate,
) error {
	return nil
}

func TestCrawlRunnerDrainsPersonalSiteQualificationsWhenIdle(
	t *testing.T,
) {
	t.Parallel()

	crawler := &drainingSourceCrawler{}
	runner, err := NewCrawlRunner(
		&fakeCrawlSourceStore{},
		crawler,
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatalf("NewCrawlRunner() error = %v", err)
	}

	report, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if report != (CrawlReport{}) || !crawler.drained {
		t.Fatalf(
			"report=%#v drained=%v",
			report,
			crawler.drained,
		)
	}
}

func TestCrawlRunnerReturnsPersonalSiteDrainFailure(
	t *testing.T,
) {
	t.Parallel()

	drainErr := errors.New("qualify failed")
	crawler := &drainingSourceCrawler{drainErr: drainErr}
	runner, err := NewCrawlRunner(
		&fakeCrawlSourceStore{},
		crawler,
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatalf("NewCrawlRunner() error = %v", err)
	}

	_, err = runner.RunOnce(context.Background())
	if !errors.Is(err, drainErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestMultiPageCrawlerDrainPersonalSiteQualifications(
	t *testing.T,
) {
	t.Parallel()

	var nilCrawler *MultiPageCrawler
	if err := nilCrawler.DrainPersonalSiteQualifications(
		context.Background(),
	); err != nil {
		t.Fatalf("nil crawler error = %v", err)
	}

	sink := &drainingSink{}
	crawler := &MultiPageCrawler{sink: sink}
	if err := crawler.DrainPersonalSiteQualifications(
		context.Background(),
	); err != nil || !sink.drained {
		t.Fatalf("drain err=%v drained=%v", err, sink.drained)
	}

	nonDrainer := &MultiPageCrawler{sink: nonDrainingSink{}}
	if err := nonDrainer.DrainPersonalSiteQualifications(
		context.Background(),
	); err != nil {
		t.Fatalf("non-drainer error = %v", err)
	}
}

func TestQualifyPersonalSiteRetryAfter(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Retry-After", "120")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyHTTPGetter{client: server.Client()},
		mustOrigin(t, server.URL),
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationHTTPError {
		t.Fatalf("outcome = %q", qualification.Outcome)
	}
	if qualification.RetryAfter != 120*time.Second {
		t.Fatalf("retry after = %v", qualification.RetryAfter)
	}
}
