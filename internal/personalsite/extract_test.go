package personalsite

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSignalsFixtures(t *testing.T) {
	t.Parallel()

	pageURL := mustParseURL(t, "https://example.com/")

	cases := []struct {
		name     string
		file     string
		want     SiteSignals
		wantStat ExtractStatus
	}{
		{
			name: "01_person_schema",
			file: "01_person_schema.html",
			want: SiteSignals{HasPersonSchema: true},
		},
		{
			name: "02_hcard_relme",
			file: "02_hcard_relme.html",
			want: SiteSignals{
				HasHCard: true,
				HasRelMe: true,
			},
		},
		{
			name: "03_feed_about_blog",
			file: "03_feed_about_blog.html",
			want: SiteSignals{
				HasFeed:         true,
				HasAboutLink:    true,
				HasBlogLink:     true,
				HasNowLink:      true,
				HasUsesLink:     true,
				HasProjectsLink: true,
			},
		},
		{
			name: "04_author_metadata",
			file: "04_author_metadata.html",
			want: SiteSignals{
				HasAuthorMetadata: true,
				HasAboutLink:      true,
			},
		},
		{
			name: "05_organization_schema",
			file: "05_organization_schema.html",
			want: SiteSignals{
				HasOrganizationSchema: true,
				HasCareersLink:        true,
				HasInvestorLink:       true,
			},
		},
		{
			name: "06_commerce",
			file: "06_commerce.html",
			want: SiteSignals{
				HasProductSchema:      true,
				HasShoppingCart:       true,
				HasMarketplaceSignals: true,
			},
		},
		{
			name: "07_marketplace_careers",
			file: "07_marketplace_careers.html",
			want: SiteSignals{
				HasMarketplaceSignals: true,
				HasCareersLink:        true,
				HasInvestorLink:       true,
			},
		},
		{
			name: "08_parked_domain",
			file: "08_parked_domain.html",
			want: SiteSignals{HasParkedDomain: true},
		},
		{
			name: "09_weak_positive_many_negatives",
			file: "09_weak_positive_many_negatives.html",
			want: SiteSignals{
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
		},
		{
			name: "10_minimal",
			file: "10_minimal.html",
			want: SiteSignals{},
		},
		{
			name: "12_mixed_personal_org",
			file: "12_mixed_personal_org.html",
			want: SiteSignals{
				HasPersonSchema:       true,
				HasOrganizationSchema: true,
				HasHCard:              true,
				HasRelMe:              true,
				HasFeed:               true,
				HasAboutLink:          true,
				HasBlogLink:           true,
				HasProjectsLink:       true,
			},
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			body := readTestdata(t, testCase.file)
			got, status, err := ExtractSignals(
				pageURL,
				"text/html; charset=utf-8",
				body,
			)
			if err != nil {
				t.Fatalf("ExtractSignals() error = %v", err)
			}
			wantStatus := ExtractStatusComplete
			if testCase.wantStat != 0 {
				wantStatus = testCase.wantStat
			}
			if status != wantStatus {
				t.Fatalf(
					"ExtractSignals() status = %v, want %v",
					status,
					wantStatus,
				)
			}
			if got != testCase.want {
				t.Fatalf(
					"ExtractSignals() = %#v, want %#v",
					got,
					testCase.want,
				)
			}
		})
	}
}

func TestExtractSignalsRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	_, _, err := ExtractSignals(nil, "text/html", []byte("<html></html>"))
	if err == nil {
		t.Fatal("ExtractSignals(nil) error = nil, want error")
	}

	pageURL := mustParseURL(t, "https://example.com/")
	_, status, err := ExtractSignals(
		pageURL,
		"application/json",
		[]byte(`{"ok":true}`),
	)
	if err != nil {
		t.Fatalf("ExtractSignals() error = %v", err)
	}
	if status != ExtractStatusUnsupportedContent {
		t.Fatalf(
			"status = %v, want unsupported",
			status,
		)
	}

	tooLarge := make([]byte, maxRawBody+1)
	_, status, err = ExtractSignals(
		pageURL,
		"text/html",
		tooLarge,
	)
	if err != nil {
		t.Fatalf("ExtractSignals() error = %v", err)
	}
	if status != ExtractStatusTooLarge {
		t.Fatalf("status = %v, want too large", status)
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("testdata", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return body
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}
