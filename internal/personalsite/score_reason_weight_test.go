package personalsite

import "testing"

func TestReasonWeightAllCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code  ReasonCode
		want  int
		known bool
	}{
		{ReasonPersonSchema, weightPersonSchema, true},
		{ReasonHCard, weightHCard, true},
		{ReasonRelMe, weightRelMe, true},
		{ReasonFeed, weightFeed, true},
		{ReasonAboutLink, weightAboutLink, true},
		{ReasonNowLink, weightNowLink, true},
		{ReasonUsesLink, weightUsesLink, true},
		{ReasonProjectsLink, weightProjectsLink, true},
		{ReasonBlogLink, weightBlogLink, true},
		{ReasonAuthorMetadata, weightAuthorMetadata, true},
		{ReasonOrganizationSchema, weightOrganizationSchema, true},
		{ReasonProductSchema, weightProductSchema, true},
		{ReasonShoppingCart, weightShoppingCart, true},
		{ReasonCareersLink, weightCareersLink, true},
		{ReasonInvestorLink, weightInvestorLink, true},
		{ReasonMarketplace, weightMarketplaceSignals, true},
		{ReasonParkedDomain, weightParkedDomain, true},
		{ReasonCode("unknown"), 0, false},
	}

	for _, testCase := range cases {
		got, ok := ReasonWeight(testCase.code)
		if got != testCase.want || ok != testCase.known {
			t.Fatalf(
				"%s = %d,%v want %d,%v",
				testCase.code,
				got,
				ok,
				testCase.want,
				testCase.known,
			)
		}
	}
}

func TestContributionsFromReasonsSkipsUnknown(t *testing.T) {
	t.Parallel()

	sum := weightPersonSchema + weightParkedDomain
	contributions, clamped := ContributionsFromReasons(
		[]ReasonCode{
			ReasonPersonSchema,
			ReasonCode("not-a-code"),
			ReasonParkedDomain,
		},
		clampScore(sum),
	)
	if len(contributions) != 2 {
		t.Fatalf("contributions = %#v", contributions)
	}
	if clamped != (clampScore(sum) != sum) {
		t.Fatalf("clamped = %v for sum %d", clamped, sum)
	}
}
