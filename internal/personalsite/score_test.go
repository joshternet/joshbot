package personalsite

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshternet/joshbot/internal/testutil"
)

func TestScorePersonalSiteWeights(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		signals SiteSignals
		want    Assessment
	}{
		{
			name:    "empty",
			signals: SiteSignals{},
			want: Assessment{
				Score:   0,
				Reasons: []ReasonCode{},
				Version: AssessmentVersion,
			},
		},
		{
			name:    "person_only",
			signals: SiteSignals{HasPersonSchema: true},
			want: Assessment{
				Score:   weightPersonSchema,
				Reasons: []ReasonCode{ReasonPersonSchema},
				Version: AssessmentVersion,
			},
		},
		{
			name: "all_positives_clamped",
			signals: SiteSignals{
				HasPersonSchema:   true,
				HasHCard:          true,
				HasRelMe:          true,
				HasFeed:           true,
				HasAboutLink:      true,
				HasNowLink:        true,
				HasUsesLink:       true,
				HasProjectsLink:   true,
				HasBlogLink:       true,
				HasAuthorMetadata: true,
			},
			want: Assessment{
				Score: 100,
				Reasons: []ReasonCode{
					ReasonAboutLink,
					ReasonAuthorMetadata,
					ReasonBlogLink,
					ReasonFeed,
					ReasonHCard,
					ReasonNowLink,
					ReasonPersonSchema,
					ReasonProjectsLink,
					ReasonRelMe,
					ReasonUsesLink,
				},
				Version: AssessmentVersion,
			},
		},
		{
			name: "negatives_clamp_to_zero",
			signals: SiteSignals{
				HasShoppingCart: true,
				HasParkedDomain: true,
			},
			want: Assessment{
				Score: 0,
				Reasons: []ReasonCode{
					ReasonParkedDomain,
					ReasonShoppingCart,
				},
				Version: AssessmentVersion,
			},
		},
		{
			name: "fixture_09_negatives_dominate",
			signals: SiteSignals{
				HasAuthorMetadata:     true,
				HasAboutLink:          true,
				HasOrganizationSchema: true,
				HasProductSchema:      true,
				HasShoppingCart:       true,
				HasMarketplaceSignals: true,
				HasCareersLink:        true,
				HasInvestorLink:       true,
				HasParkedDomain:       true,
			},
			want: Assessment{
				Score: 0,
				Reasons: []ReasonCode{
					ReasonAboutLink,
					ReasonAuthorMetadata,
					ReasonCareersLink,
					ReasonInvestorLink,
					ReasonMarketplace,
					ReasonOrganizationSchema,
					ReasonParkedDomain,
					ReasonProductSchema,
					ReasonShoppingCart,
				},
				Version: AssessmentVersion,
			},
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := ScorePersonalSite(testCase.signals)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf(
					"ScorePersonalSite() = %#v, want %#v",
					got,
					testCase.want,
				)
			}
		})
	}
}

func TestScorePersonalSiteFixtureGoldens(t *testing.T) {
	t.Parallel()

	pageURL := mustParseURL(t, "https://example.com/")
	files := []string{
		"01_person_schema.html",
		"02_hcard_relme.html",
		"03_feed_about_blog.html",
		"04_author_metadata.html",
		"05_organization_schema.html",
		"06_commerce.html",
		"07_marketplace_careers.html",
		"08_parked_domain.html",
		"09_weak_positive_many_negatives.html",
		"10_minimal.html",
		"12_mixed_personal_org.html",
	}

	for _, file := range files {
		file := file
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			body := readTestdata(t, file)
			signals, status, err := ExtractSignals(
				pageURL,
				"text/html; charset=utf-8",
				body,
			)
			if err != nil {
				t.Fatalf("ExtractSignals() error = %v", err)
			}
			if status != ExtractStatusComplete {
				t.Fatalf(
					"ExtractSignals() status = %v",
					status,
				)
			}

			assessment := ScorePersonalSite(signals)
			payload, err := json.MarshalIndent(
				goldenAssessment{
					Signals:    signals,
					Assessment: assessment,
				},
				"",
				"  ",
			)
			if err != nil {
				t.Fatalf("MarshalIndent() error = %v", err)
			}
			payload = append(payload, '\n')

			goldenPath := filepath.Join(
				"testdata",
				file+".golden.json",
			)
			if err := testutil.CheckGolden(
				goldenPath,
				payload,
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScoreBucket(t *testing.T) {
	t.Parallel()

	cases := map[int]string{
		0:   "0-19",
		19:  "0-19",
		20:  "20-39",
		39:  "20-39",
		40:  "40-59",
		59:  "40-59",
		60:  "60-79",
		79:  "60-79",
		80:  "80-100",
		100: "80-100",
	}
	for score, want := range cases {
		if got := ScoreBucket(score); got != want {
			t.Fatalf(
				"ScoreBucket(%d) = %q, want %q",
				score,
				got,
				want,
			)
		}
	}
}

type goldenAssessment struct {
	Signals    SiteSignals `json:"signals"`
	Assessment Assessment  `json:"assessment"`
}
