package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

const (
	// personalSiteQualifyMaxPageBytes bounds the root-page body for scoring.
	personalSiteQualifyMaxPageBytes = MaxRawBody
)

// extractPersonalSiteSignals is personalsite.ExtractSignals; tests may replace it.
var extractPersonalSiteSignals = personalsite.ExtractSignals

// QualifyPersonalSite fetches one origin root page through getter and scores
// structural signals. Network or parse failures return Outcome != assessed
// without inventing a zero score.
//
// @param ctx - Request context; cancellation maps to timeout.
// @param getter - Robots-aware guarded HTTP client.
// @param candidate - Canonical origin to inspect at "/".
// @returns Qualification with typed outcome; error only for invalid args.
func QualifyPersonalSite(
	ctx context.Context,
	getter Getter,
	candidate origin.Origin,
) (personalsite.Qualification, error) {
	if ctx == nil {
		return personalsite.Qualification{}, errInvalidContext
	}
	if getter == nil {
		return personalsite.Qualification{}, errGetterUnavailable
	}
	if candidate.String() == "" {
		return personalsite.Qualification{}, errInvalidSource
	}

	rootURL := rootPageURL(candidate)

	response, getErr := getter.Get(ctx, rootURL)
	if getErr != nil {
		return mapQualifyFetchError(ctx, getErr), nil
	}
	if response == nil || response.Body == nil {
		return personalsite.Qualification{
			Outcome: personalsite.QualificationUnavailable,
		}, nil
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return personalsite.Qualification{
			Outcome: personalsite.QualificationHTTPError,
		}, nil
	}

	body, tooLarge, readErr := readBounded(
		response.Body,
		personalSiteQualifyMaxPageBytes,
	)
	if readErr != nil {
		if ctx.Err() != nil {
			return personalsite.Qualification{
				Outcome: personalsite.QualificationTimeout,
			}, nil
		}
		return personalsite.Qualification{
			Outcome: personalsite.QualificationUnavailable,
		}, nil
	}
	if tooLarge {
		return personalsite.Qualification{
			Outcome: personalsite.QualificationTooLarge,
		}, nil
	}

	// Drain any remaining body without retaining it.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1))

	signals, status, _ := extractPersonalSiteSignals(
		rootURL,
		response.Header.Get("Content-Type"),
		body,
	)

	switch status {
	case personalsite.ExtractStatusComplete:
		assessment := personalsite.ScorePersonalSite(signals)
		return personalsite.Qualification{
			Outcome:    personalsite.QualificationAssessed,
			Assessment: assessment,
		}, nil
	case personalsite.ExtractStatusUnsupportedContent:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationUnsupportedContent,
		}, nil
	case personalsite.ExtractStatusTooLarge:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationTooLarge,
		}, nil
	case personalsite.ExtractStatusUnavailable:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationParserFailure,
		}, nil
	default:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationUnavailable,
		}, nil
	}
}

func rootPageURL(candidate origin.Origin) *url.URL {
	// Canonical origins always produce a parseable absolute URL.
	parsed, _ := url.Parse(candidate.String() + "/")
	return parsed
}

func mapQualifyFetchError(
	ctx context.Context,
	err error,
) personalsite.Qualification {
	switch {
	case errors.Is(err, robots.ErrDisallowed):
		return personalsite.Qualification{
			Outcome: personalsite.QualificationRobotsDenied,
		}
	case ctx.Err() != nil:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationTimeout,
		}
	}

	category := netguard.FailureCategory(err)
	switch category {
	case retry.CategoryDNS:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationDNSFailure,
		}
	case retry.CategoryUnsafeAddress:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationNetworkRejected,
		}
	case retry.CategoryTimeout:
		return personalsite.Qualification{
			Outcome: personalsite.QualificationTimeout,
		}
	default:
		if errors.Is(err, robots.ErrTemporary) {
			return personalsite.Qualification{
				Outcome: personalsite.QualificationUnavailable,
			}
		}
		return personalsite.Qualification{
			Outcome: personalsite.QualificationUnavailable,
		}
	}
}
