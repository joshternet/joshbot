package declaration

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
)

// WellKnownPath is the canonical RFC-JOSH-0002 declaration path.
const WellKnownPath = "/.well-known/josh"

// Five redirects is a JoshBot resource-safety policy rather than an
// RFC-JOSH-0002 protocol limit.
const maxDeclarationRedirects = 5

var (
	errVerifierUnavailable = errors.New(
		"declaration: verifier unavailable",
	)
	errDeclarationGetterUnavailable = errors.New(
		"declaration: getter unavailable",
	)
	errInvalidDeclarationContext = errors.New(
		"declaration: invalid context",
	)
	errInvalidDeclarationOrigin = errors.New(
		"declaration: invalid origin",
	)
	errInvalidDeclarationRedirect = errors.New(
		"declaration: invalid redirect",
	)
	errCrossOriginDeclarationRedirect = errors.New(
		"declaration: cross-origin redirect",
	)
)

// Outcome classifies declaration verification results.
type Outcome uint8

const (
	// OutcomeInvalid reports an invalid declaration body.
	OutcomeInvalid Outcome = iota

	// OutcomeValid reports a valid RFC-JOSH-0002 version 1 declaration.
	OutcomeValid

	// OutcomeAbsent reports that the origin returned 404 or 410.
	OutcomeAbsent

	// OutcomeUnsupportedVersion reports a declaration using an unsupported
	// integer version.
	OutcomeUnsupportedVersion

	// OutcomeUnavailable reports that the declaration could not be
	// completely retrieved.
	OutcomeUnavailable

	// OutcomeRobotsDenied reports that robots policy denied retrieval.
	OutcomeRobotsDenied

	// OutcomeCrossOriginRedirect reports that the origin redirected to a
	// resource without authority to speak for the original origin.
	OutcomeCrossOriginRedirect
)

// Result contains the semantic outcome of declaration verification.
//
// Declaration is populated only when Outcome is OutcomeValid. Raw response
// bytes and unknown JSON members are not retained.
type Result struct {
	Outcome     Outcome
	Origin      origin.Origin
	Declaration Declaration
}

// Getter is the robots-aware guarded retrieval behavior required by Verifier.
type Getter interface {
	Get(
		context.Context,
		*url.URL,
	) (*http.Response, error)
}

// Verifier retrieves and validates canonical Joshternet declarations.
type Verifier struct {
	getter Getter
}

var _ Getter = (*robots.Checker)(nil)

// NewVerifier constructs a declaration verifier.
//
// getter should be a robots.Checker in production.
func NewVerifier(getter Getter) *Verifier {
	return &Verifier{
		getter: getter,
	}
}

// Verify retrieves and validates the canonical declaration for source.
//
// Remote HTTP and protocol conditions are returned as Result outcomes.
// Programmer misuse and context cancellation are returned as Go errors.
func (v *Verifier) Verify(
	ctx context.Context,
	source origin.Origin,
) (Result, error) {
	if v == nil {
		return Result{}, errVerifierUnavailable
	}

	if ctx == nil {
		return Result{}, errInvalidDeclarationContext
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	if source.String() == "" {
		return Result{}, errInvalidDeclarationOrigin
	}

	if v.getter == nil {
		return Result{}, errDeclarationGetterUnavailable
	}

	current, _ := url.Parse(
		source.String() + WellKnownPath,
	)
	redirects := 0

	for {
		response, err := v.getter.Get(ctx, current)
		if err != nil {
			closeDeclarationResponse(response)

			if contextError := ctx.Err(); contextError != nil {
				return Result{}, contextError
			}

			if errors.Is(err, robots.ErrDisallowed) {
				return Result{
					Outcome: OutcomeRobotsDenied,
					Origin:  source,
				}, nil
			}

			return Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			}, nil
		}

		if response == nil || response.Body == nil {
			closeDeclarationResponse(response)

			return Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			}, nil
		}

		status := response.StatusCode

		if isDeclarationRedirect(status) {
			_ = response.Body.Close()

			if redirects >= maxDeclarationRedirects {
				return Result{
					Outcome: OutcomeUnavailable,
					Origin:  source,
				}, nil
			}

			next, redirectError := declarationRedirectTarget(
				current,
				response,
				source,
			)
			if redirectError != nil {
				if errors.Is(
					redirectError,
					errCrossOriginDeclarationRedirect,
				) {
					return Result{
						Outcome: OutcomeCrossOriginRedirect,
						Origin:  source,
					}, nil
				}

				return Result{
					Outcome: OutcomeUnavailable,
					Origin:  source,
				}, nil
			}

			current = next
			redirects++

			continue
		}

		switch {
		case status >= 200 && status <= 299:
			data, readError := readDeclarationBody(
				response.Body,
			)
			_ = response.Body.Close()

			if readError != nil {
				if contextError := ctx.Err(); contextError != nil {
					return Result{}, contextError
				}

				if errors.Is(
					readError,
					errDeclarationBodyTooLarge,
				) {
					return Result{
						Outcome: OutcomeInvalid,
						Origin:  source,
					}, nil
				}

				return Result{
					Outcome: OutcomeUnavailable,
					Origin:  source,
				}, nil
			}

			parsed := Parse(data)
			switch parsed.Status {
			case ParseValid:
				return Result{
					Outcome:     OutcomeValid,
					Origin:      source,
					Declaration: parsed.Declaration,
				}, nil
			case ParseUnsupportedVersion:
				return Result{
					Outcome: OutcomeUnsupportedVersion,
					Origin:  source,
				}, nil
			default:
				return Result{
					Outcome: OutcomeInvalid,
					Origin:  source,
				}, nil
			}

		case status == http.StatusNotFound ||
			status == http.StatusGone:
			_ = response.Body.Close()

			return Result{
				Outcome: OutcomeAbsent,
				Origin:  source,
			}, nil

		default:
			_ = response.Body.Close()

			return Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			}, nil
		}
	}
}

func isDeclarationRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func declarationRedirectTarget(
	current *url.URL,
	response *http.Response,
	source origin.Origin,
) (*url.URL, error) {
	location := response.Header.Get("Location")
	if location == "" {
		return nil, errInvalidDeclarationRedirect
	}

	next, err := current.Parse(location)
	if err != nil {
		return nil, errInvalidDeclarationRedirect
	}

	next.Fragment = ""
	next.RawFragment = ""

	nextOrigin, err := origin.Parse(next.String())
	if err != nil {
		return nil, errInvalidDeclarationRedirect
	}

	if nextOrigin != source {
		return nil, errCrossOriginDeclarationRedirect
	}

	return next, nil
}

func closeDeclarationResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}
