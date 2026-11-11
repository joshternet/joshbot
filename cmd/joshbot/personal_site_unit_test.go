package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
	"github.com/joshternet/joshbot/internal/store"
)

type personalSiteAssessmentFake struct {
	candidates []origin.Origin
	listErr    error
	upsertErr  error
	upserted   []origin.Origin
}

func (fake *personalSiteAssessmentFake) CandidatesNeedingPersonalSiteAssessment(
	context.Context,
	int,
) ([]origin.Origin, error) {
	if fake.listErr != nil {
		return nil, fake.listErr
	}
	return fake.candidates, nil
}

func (fake *personalSiteAssessmentFake) UpsertPersonalSiteAssessment(
	_ context.Context,
	candidate origin.Origin,
	_ personalsite.Qualification,
) error {
	fake.upserted = append(fake.upserted, candidate)
	return fake.upsertErr
}

type personalSiteAdmissionFake struct {
	*personalSiteAssessmentFake
}

func (personalSiteAdmissionFake) PendingAutomaticCandidates(
	context.Context,
	discovery.CrawlRunID,
) ([]discovery.Candidate, error) {
	return nil, nil
}

func (personalSiteAdmissionFake) CompleteAutomaticCandidates(
	context.Context,
	discovery.CrawlRunID,
	[]store.AutomaticCandidateResult,
) error {
	return nil
}

func (personalSiteAdmissionFake) RecordDiscovery(
	context.Context,
	origin.Origin,
	[]discovery.Candidate,
) (discovery.RecordResult, error) {
	return discovery.RecordResult{}, nil
}

type admissionOnlyFake struct{}

func (admissionOnlyFake) PendingAutomaticCandidates(
	context.Context,
	discovery.CrawlRunID,
) ([]discovery.Candidate, error) {
	return nil, nil
}

func (admissionOnlyFake) CompleteAutomaticCandidates(
	context.Context,
	discovery.CrawlRunID,
	[]store.AutomaticCandidateResult,
) error {
	return nil
}

func (admissionOnlyFake) RecordDiscovery(
	context.Context,
	origin.Origin,
	[]discovery.Candidate,
) (discovery.RecordResult, error) {
	return discovery.RecordResult{}, nil
}

type personalSiteGetterFunc func(context.Context, *url.URL) (*http.Response, error)

func (getter personalSiteGetterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter(ctx, target)
}

func TestQualifyPersonalSiteCandidates(t *testing.T) {
	t.Parallel()

	candidate := mustParseTestOrigin(t, "https://personal.example")

	t.Run("inactive_or_nil_getter", func(t *testing.T) {
		t.Parallel()
		sink := runtimeCandidateSink{
			scoring: personalsite.ScoringModeOff,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}

		sink = runtimeCandidateSink{
			scoring: personalsite.ScoringModeShadow,
			getter:  nil,
		}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("store_without_assessment_api", func(t *testing.T) {
		t.Parallel()
		sink := runtimeCandidateSink{
			store:   admissionOnlyFake{},
			scoring: personalsite.ScoringModeShadow,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("list_error", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			listErr: errors.New("list failed"),
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			scoring:                personalsite.ScoringModeShadow,
			maxAutomaticPromotions: 3,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		err := sink.qualifyPersonalSiteCandidates(context.Background())
		if err == nil || !strings.Contains(err.Error(), "list failed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("qualify_error", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			candidates: []origin.Origin{{}},
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			scoring: personalsite.ScoringModeOrder,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		err := sink.qualifyPersonalSiteCandidates(context.Background())
		if err == nil {
			t.Fatal("error = nil, want qualify error")
		}
	})

	t.Run("upsert_error", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			candidates: []origin.Origin{candidate},
			upsertErr:  errors.New("upsert failed"),
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			scoring: personalsite.ScoringModeShadow,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body: io.NopCloser(strings.NewReader(
						`<!doctype html><html><body><a href="/about">a</a></body></html>`,
					)),
				}, nil
			}),
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		err := sink.qualifyPersonalSiteCandidates(context.Background())
		if err == nil || !strings.Contains(err.Error(), "upsert failed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("success_with_log", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			candidates: []origin.Origin{candidate},
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			scoring:                personalsite.ScoringModeShadow,
			maxAutomaticPromotions: 0,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body: io.NopCloser(strings.NewReader(
						`<!doctype html><html><body><a href="/about">a</a></body></html>`,
					)),
				}, nil
			}),
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}
		if len(fake.upserted) != 1 {
			t.Fatalf("upserted = %#v", fake.upserted)
		}
	})
}

func TestPersonalSiteQualificationLimit(t *testing.T) {
	t.Parallel()

	if got := personalSiteQualificationLimit(0); got != 200 {
		t.Fatalf("limit(0) = %d, want 200", got)
	}
	if got := personalSiteQualificationLimit(50); got != 100 {
		t.Fatalf("limit(50) = %d, want 100", got)
	}
	if got := personalSiteQualificationLimit(150); got != 200 {
		t.Fatalf("limit(150) = %d, want 200", got)
	}
}

func TestFinalizeCandidatesQualifyError(t *testing.T) {
	t.Parallel()

	fake := &personalSiteAssessmentFake{
		listErr: errors.New("qualify list failed"),
	}
	sink := runtimeCandidateSink{
		store: personalSiteAdmissionFake{
			personalSiteAssessmentFake: fake,
		},
		scoring: personalsite.ScoringModeShadow,
		getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, errors.New("unused")
		}),
	}

	err := sink.FinalizeCandidates(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "qualify list failed") {
		t.Fatalf("error = %v", err)
	}
}

func mustParseTestOrigin(t *testing.T, raw string) origin.Origin {
	t.Helper()
	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", raw, err)
	}
	return parsed
}
