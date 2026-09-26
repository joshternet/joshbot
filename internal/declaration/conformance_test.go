package declaration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

const rfcJOSH0002ConformanceDirEnv = "JOSHBOT_RFC_JOSH_0002_CONFORMANCE_DIR"

var rfcJOSH0002FixtureIDPattern = regexp.MustCompile(
	`^[a-z0-9]+(?:-[a-z0-9]+)*$`,
)

type rfcJOSH0002Fixture struct {
	ID          string                    `json:"id"`
	Description string                    `json:"description"`
	Role        string                    `json:"role"`
	Conformance string                    `json:"conformance"`
	Capability  string                    `json:"capability"`
	RFCSections []string                  `json:"rfc_sections"`
	Origin      string                    `json:"origin"`
	Exchanges   []rfcJOSH0002Exchange     `json:"exchanges"`
	Expected    rfcJOSH0002ExpectedResult `json:"expected"`
}

type rfcJOSH0002Exchange struct {
	Request  rfcJOSH0002Request   `json:"request"`
	Response *rfcJOSH0002Response `json:"response"`
	Failure  *rfcJOSH0002Failure  `json:"failure"`
}

type rfcJOSH0002Request struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}

type rfcJOSH0002Response struct {
	Status  int                 `json:"status"`
	Headers []rfcJOSH0002Header `json:"headers"`
	Body    string              `json:"body"`
}

type rfcJOSH0002Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type rfcJOSH0002Failure struct {
	Kind string `json:"kind"`
}

type rfcJOSH0002ExpectedResult struct {
	Declaration   string `json:"declaration"`
	Participation string `json:"participation"`
	Identity      string `json:"identity"`
}

func TestRFCJOSH0002CanonicalConformance(t *testing.T) {
	directory := os.Getenv(rfcJOSH0002ConformanceDirEnv)
	if directory == "" {
		t.Skipf(
			"set %s to run the canonical RFC-JOSH-0002 corpus",
			rfcJOSH0002ConformanceDirEnv,
		)
	}

	fixtures, err := loadRFCJOSH0002Fixtures(directory)
	if err != nil {
		t.Fatalf(
			"load RFC-JOSH-0002 conformance fixtures: %v",
			err,
		)
	}

	if len(fixtures) == 0 {
		t.Fatalf(
			"RFC-JOSH-0002 conformance directory %q contains no fixtures",
			directory,
		)
	}

	for _, fixture := range fixtures {
		fixture := fixture

		t.Run(fixture.ID, func(t *testing.T) {
			if fixture.Role != "consumer" {
				t.Skipf(
					"fixture role %q is not exercised by JoshBot",
					fixture.Role,
				)
			}

			if fixture.Conformance == "optional-capability" &&
				!supportsRFCJOSH0002Capability(fixture.Capability) {
				t.Skipf(
					"JoshBot does not claim optional capability %q",
					fixture.Capability,
				)
			}

			runRFCJOSH0002Fixture(t, fixture)
		})
	}
}

func loadRFCJOSH0002Fixtures(
	directory string,
) ([]rfcJOSH0002Fixture, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf(
			"read fixture directory %q: %w",
			directory,
			err,
		)
	}

	fixtures := make([]rfcJOSH0002Fixture, 0, len(entries))
	ids := make(map[string]string)

	for _, entry := range entries {
		if entry.IsDir() ||
			filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf(
				"read fixture %q: %w",
				path,
				err,
			)
		}

		var fixture rfcJOSH0002Fixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			return nil, fmt.Errorf(
				"decode fixture %q: %w",
				path,
				err,
			)
		}

		if err := validateRFCJOSH0002Fixture(
			entry.Name(),
			fixture,
		); err != nil {
			return nil, err
		}

		if previous, exists := ids[fixture.ID]; exists {
			return nil, fmt.Errorf(
				"fixture %q duplicates id %q already used by %q",
				entry.Name(),
				fixture.ID,
				previous,
			)
		}

		ids[fixture.ID] = entry.Name()
		fixtures = append(fixtures, fixture)
	}

	return fixtures, nil
}

func validateRFCJOSH0002Fixture(
	filename string,
	fixture rfcJOSH0002Fixture,
) error {
	if !rfcJOSH0002FixtureIDPattern.MatchString(fixture.ID) {
		return fmt.Errorf(
			"fixture %q has invalid id %q",
			filename,
			fixture.ID,
		)
	}

	wantFilename := fixture.ID + ".json"
	if filename != wantFilename {
		return fmt.Errorf(
			"fixture filename = %q, want %q for id %q",
			filename,
			wantFilename,
			fixture.ID,
		)
	}

	switch fixture.Role {
	case "consumer", "publisher":
	default:
		return fmt.Errorf(
			"fixture %q has unsupported role %q",
			fixture.ID,
			fixture.Role,
		)
	}

	switch fixture.Conformance {
	case "required", "recommended":
		if fixture.Capability != "" {
			return fmt.Errorf(
				"fixture %q has capability %q with conformance %q",
				fixture.ID,
				fixture.Capability,
				fixture.Conformance,
			)
		}
	case "optional-capability":
		if fixture.Capability == "" {
			return fmt.Errorf(
				"fixture %q is optional-capability without a capability",
				fixture.ID,
			)
		}
	default:
		return fmt.Errorf(
			"fixture %q has unsupported conformance %q",
			fixture.ID,
			fixture.Conformance,
		)
	}

	if len(fixture.RFCSections) == 0 {
		return fmt.Errorf(
			"fixture %q has no RFC-JOSH-0002 section references",
			fixture.ID,
		)
	}

	if fixture.Origin == "" {
		return fmt.Errorf(
			"fixture %q has an empty origin",
			fixture.ID,
		)
	}

	if len(fixture.Exchanges) == 0 {
		return fmt.Errorf(
			"fixture %q has no exchanges",
			fixture.ID,
		)
	}

	for index, exchange := range fixture.Exchanges {
		if exchange.Request.Method != http.MethodGet {
			return fmt.Errorf(
				"fixture %q exchange %d method = %q, want GET",
				fixture.ID,
				index,
				exchange.Request.Method,
			)
		}

		if exchange.Request.URI == "" {
			return fmt.Errorf(
				"fixture %q exchange %d has an empty request URI",
				fixture.ID,
				index,
			)
		}

		hasResponse := exchange.Response != nil
		hasFailure := exchange.Failure != nil
		if hasResponse == hasFailure {
			return fmt.Errorf(
				"fixture %q exchange %d must contain exactly one response or failure",
				fixture.ID,
				index,
			)
		}

		if exchange.Response != nil {
			if exchange.Response.Status < 100 ||
				exchange.Response.Status > 599 {
				return fmt.Errorf(
					"fixture %q exchange %d has invalid HTTP status %d",
					fixture.ID,
					index,
					exchange.Response.Status,
				)
			}

			for headerIndex, header := range exchange.Response.Headers {
				if header.Name == "" {
					return fmt.Errorf(
						"fixture %q exchange %d header %d has an empty name",
						fixture.ID,
						index,
						headerIndex,
					)
				}
			}
		}

		if exchange.Failure != nil {
			switch exchange.Failure.Kind {
			case "dns", "tls", "timeout", "transport":
			default:
				return fmt.Errorf(
					"fixture %q exchange %d has unsupported failure kind %q",
					fixture.ID,
					index,
					exchange.Failure.Kind,
				)
			}
		}
	}

	if fixture.Expected.Declaration == "" ||
		fixture.Expected.Participation == "" ||
		fixture.Expected.Identity == "" {
		return fmt.Errorf(
			"fixture %q has an incomplete expected result",
			fixture.ID,
		)
	}

	return nil
}

func supportsRFCJOSH0002Capability(
	capability string,
) bool {
	return capability == "same-origin-redirects"
}

func runRFCJOSH0002Fixture(
	t *testing.T,
	fixture rfcJOSH0002Fixture,
) {
	t.Helper()

	source := mustDeclarationOrigin(t, fixture.Origin)

	steps := make(
		[]declarationGetterStep,
		0,
		len(fixture.Exchanges),
	)
	wantTargets := make(
		[]string,
		0,
		len(fixture.Exchanges),
	)

	for _, exchange := range fixture.Exchanges {
		wantTargets = append(
			wantTargets,
			exchange.Request.URI,
		)

		if exchange.Response != nil {
			response := declarationTextResponse(
				exchange.Response.Status,
				exchange.Response.Body,
			)

			for _, header := range exchange.Response.Headers {
				response.Header.Add(
					header.Name,
					header.Value,
				)
			}

			steps = append(
				steps,
				declarationGetterStep{
					response: response,
				},
			)

			continue
		}

		steps = append(
			steps,
			declarationGetterStep{
				err: errors.New(
					"RFC-JOSH-0002 conformance " +
						exchange.Failure.Kind +
						" failure",
				),
			},
		)
	}

	getter := newScriptedDeclarationGetter(
		t,
		steps...,
	)

	result, err := NewVerifier(getter).Verify(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"fixture %q Verify() error = %v, want nil",
			fixture.ID,
			err,
		)
	}

	if result.Origin != source {
		t.Errorf(
			"fixture %q result origin = %q, want %q",
			fixture.ID,
			result.Origin.String(),
			source.String(),
		)
	}

	if !slices.Equal(getter.targets, wantTargets) {
		t.Errorf(
			"fixture %q getter targets = %q, want %q",
			fixture.ID,
			getter.targets,
			wantTargets,
		)
	}

	if len(getter.steps) != 0 {
		t.Errorf(
			"fixture %q left %d expected exchange(s) unused",
			fixture.ID,
			len(getter.steps),
		)
	}

	got, err := classifyRFCJOSH0002Result(result)
	if err != nil {
		t.Fatalf(
			"fixture %q classification error: %v",
			fixture.ID,
			err,
		)
	}

	if got != fixture.Expected {
		t.Errorf(
			"fixture %q classification = %#v, want %#v",
			fixture.ID,
			got,
			fixture.Expected,
		)
	}

	assertDeclarationBodiesClosed(t, getter)
}

func classifyRFCJOSH0002Result(
	result Result,
) (rfcJOSH0002ExpectedResult, error) {
	switch result.Outcome {
	case OutcomeValid:
		if result.Declaration.Version != 1 {
			return rfcJOSH0002ExpectedResult{}, fmt.Errorf(
				"valid declaration version = %d, want 1",
				result.Declaration.Version,
			)
		}

		identity, err := classifyRFCJOSH0002Identity(
			result.Declaration.Identity,
		)
		if err != nil {
			return rfcJOSH0002ExpectedResult{}, err
		}

		return rfcJOSH0002ExpectedResult{
			Declaration:   "valid-v1",
			Participation: "declared",
			Identity:      identity,
		}, nil

	case OutcomeInvalid:
		return rfcJOSH0002ExpectedResult{
			Declaration:   "invalid",
			Participation: "not-declared",
			Identity:      "not-applicable",
		}, nil

	case OutcomeAbsent:
		return rfcJOSH0002ExpectedResult{
			Declaration:   "absent",
			Participation: "not-declared",
			Identity:      "not-applicable",
		}, nil

	case OutcomeUnsupportedVersion:
		return rfcJOSH0002ExpectedResult{
			Declaration:   "unsupported-version",
			Participation: "indeterminate",
			Identity:      "not-applicable",
		}, nil

	case OutcomeUnavailable:
		return rfcJOSH0002ExpectedResult{
			Declaration:   "temporary-failure",
			Participation: "indeterminate",
			Identity:      "not-applicable",
		}, nil

	case OutcomeCrossOriginRedirect:
		return rfcJOSH0002ExpectedResult{
			Declaration:   "cross-origin-redirect",
			Participation: "not-declared",
			Identity:      "not-applicable",
		}, nil

	default:
		return rfcJOSH0002ExpectedResult{}, fmt.Errorf(
			"JoshBot outcome %d has no RFC-JOSH-0002 conformance mapping",
			result.Outcome,
		)
	}
}

func classifyRFCJOSH0002Identity(
	identity Identity,
) (string, error) {
	switch identity {
	case IdentityAffirmed:
		return "affirmed", nil
	case IdentityDeclined:
		return "declined", nil
	case IdentityUndeclared:
		return "undeclared", nil
	default:
		return "", fmt.Errorf(
			"JoshBot identity %d has no RFC-JOSH-0002 conformance mapping",
			identity,
		)
	}
}
