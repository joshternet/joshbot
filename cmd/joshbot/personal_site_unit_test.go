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
	claims      []store.PersonalSiteQualifyClaim
	claimErr    error
	completeErr error
	completed   []origin.Origin
}

func (fake *personalSiteAssessmentFake) ClaimPersonalSiteQualifications(
	context.Context,
	string,
	int,
) ([]store.PersonalSiteQualifyClaim, error) {
	if fake.claimErr != nil {
		return nil, fake.claimErr
	}
	return fake.claims, nil
}

func (fake *personalSiteAssessmentFake) CompletePersonalSiteQualification(
	_ context.Context,
	claim store.PersonalSiteQualifyClaim,
	_ personalsite.Qualification,
) error {
	fake.completed = append(fake.completed, claim.Origin)
	return fake.completeErr
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

	candidate := mustParseOrigin(t, "https://personal.example")
	claim := store.PersonalSiteQualifyClaim{
		Origin:     candidate,
		Generation: 1,
		Owner:      "discovery",
	}

	t.Run("nil_getter", func(t *testing.T) {
		t.Parallel()
		sink := runtimeCandidateSink{getter: nil}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("store_without_assessment_api", func(t *testing.T) {
		t.Parallel()
		sink := runtimeCandidateSink{
			store: admissionOnlyFake{},
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("claim_error", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			claimErr: errors.New("claim failed"),
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			maxAutomaticPromotions: 3,
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		err := sink.qualifyPersonalSiteCandidates(context.Background())
		if err == nil || !strings.Contains(err.Error(), "claim failed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("qualify_error", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			claims: []store.PersonalSiteQualifyClaim{{
				Origin:     origin.Origin{},
				Generation: 1,
				Owner:      "discovery",
			}},
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
		}
		err := sink.qualifyPersonalSiteCandidates(context.Background())
		if err == nil {
			t.Fatal("error = nil, want qualify error")
		}
	})

	t.Run("claim_lost_continue", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			claims:      []store.PersonalSiteQualifyClaim{claim},
			completeErr: store.ErrPersonalSiteClaimLost,
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
			getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body: io.NopCloser(strings.NewReader(
						`<!doctype html><html><body><a href="/about">a</a></body></html>`,
					)),
				}, nil
			}),
		}
		if err := sink.qualifyPersonalSiteCandidates(context.Background()); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("complete_error", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			claims:      []store.PersonalSiteQualifyClaim{claim},
			completeErr: errors.New("complete failed"),
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
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
		if err == nil || !strings.Contains(err.Error(), "complete failed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("success_with_log", func(t *testing.T) {
		t.Parallel()
		fake := &personalSiteAssessmentFake{
			claims: []store.PersonalSiteQualifyClaim{claim},
		}
		sink := runtimeCandidateSink{
			store: personalSiteAdmissionFake{
				personalSiteAssessmentFake: fake,
			},
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
		if len(fake.completed) != 1 {
			t.Fatalf("completed = %#v", fake.completed)
		}
	})
}

func TestDrainPersonalSiteQualifications(t *testing.T) {
	t.Parallel()

	fake := &personalSiteAssessmentFake{}
	sink := runtimeCandidateSink{
		store: personalSiteAdmissionFake{
			personalSiteAssessmentFake: fake,
		},
		getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, errors.New("unused")
		}),
	}
	if err := sink.DrainPersonalSiteQualifications(context.Background()); err != nil {
		t.Fatalf("error = %v", err)
	}
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
		claimErr: errors.New("qualify claim failed"),
	}
	sink := runtimeCandidateSink{
		store: personalSiteAdmissionFake{
			personalSiteAssessmentFake: fake,
		},
		getter: personalSiteGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, errors.New("unused")
		}),
	}

	err := sink.FinalizeCandidates(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "qualify claim failed") {
		t.Fatalf("error = %v", err)
	}
}
