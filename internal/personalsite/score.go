package personalsite

import "sort"

// Weight constants for AssessmentVersion 1.
const (
	weightPersonSchema       = 25
	weightHCard              = 20
	weightRelMe              = 20
	weightFeed               = 15
	weightAboutLink          = 10
	weightNowLink            = 8
	weightUsesLink           = 8
	weightProjectsLink       = 10
	weightBlogLink           = 10
	weightAuthorMetadata     = 8
	weightOrganizationSchema = -25
	weightProductSchema      = -20
	weightShoppingCart       = -30
	weightCareersLink        = -20
	weightInvestorLink       = -20
	weightMarketplaceSignals = -25
	weightParkedDomain       = -40
)

// ScorePersonalSite maps structural site signals to a deterministic
// Assessment. The function is pure: it performs no I/O and never mutates
// inputs.
//
// @param signals - Boolean structural features from ExtractSignals.
// @returns Assessment with Score in [0,100], sorted ReasonCodes, and
// AssessmentVersion.
func ScorePersonalSite(signals SiteSignals) Assessment {
	contributions := make([]contribution, 0, 16)

	addPositive := func(active bool, weight int, code ReasonCode) {
		if !active || weight == 0 {
			return
		}
		contributions = append(contributions, contribution{
			weight: weight,
			code:   code,
		})
	}
	addNegative := func(active bool, weight int, code ReasonCode) {
		if !active || weight == 0 {
			return
		}
		contributions = append(contributions, contribution{
			weight: weight,
			code:   code,
		})
	}

	addPositive(signals.HasPersonSchema, weightPersonSchema, ReasonPersonSchema)
	addPositive(signals.HasHCard, weightHCard, ReasonHCard)
	addPositive(signals.HasRelMe, weightRelMe, ReasonRelMe)
	addPositive(signals.HasFeed, weightFeed, ReasonFeed)
	addPositive(signals.HasAboutLink, weightAboutLink, ReasonAboutLink)
	addPositive(signals.HasNowLink, weightNowLink, ReasonNowLink)
	addPositive(signals.HasUsesLink, weightUsesLink, ReasonUsesLink)
	addPositive(signals.HasProjectsLink, weightProjectsLink, ReasonProjectsLink)
	addPositive(signals.HasBlogLink, weightBlogLink, ReasonBlogLink)
	addPositive(signals.HasAuthorMetadata, weightAuthorMetadata, ReasonAuthorMetadata)

	addNegative(signals.HasOrganizationSchema, weightOrganizationSchema, ReasonOrganizationSchema)
	addNegative(signals.HasProductSchema, weightProductSchema, ReasonProductSchema)
	addNegative(signals.HasShoppingCart, weightShoppingCart, ReasonShoppingCart)
	addNegative(signals.HasCareersLink, weightCareersLink, ReasonCareersLink)
	addNegative(signals.HasInvestorLink, weightInvestorLink, ReasonInvestorLink)
	addNegative(signals.HasMarketplaceSignals, weightMarketplaceSignals, ReasonMarketplace)
	addNegative(signals.HasParkedDomain, weightParkedDomain, ReasonParkedDomain)

	total := 0
	reasons := make([]ReasonCode, 0, len(contributions))
	for _, item := range contributions {
		total += item.weight
		reasons = append(reasons, item.code)
	}

	sort.Slice(reasons, func(left, right int) bool {
		return reasons[left] < reasons[right]
	})

	score := clampScore(total)
	return Assessment{
		Score:   score,
		Reasons: reasons,
		Version: AssessmentVersion,
	}
}

type contribution struct {
	weight int
	code   ReasonCode
}

func clampScore(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

// ScoreContribution is one reason code and its AssessmentVersion weight.
type ScoreContribution struct {
	Code   ReasonCode `json:"code"`
	Weight int        `json:"weight"`
}

// ReasonWeight returns the AssessmentVersion weight for a reason code.
//
// @param code - Observed reason code.
// @returns Weight and whether the code is known for this version.
func ReasonWeight(code ReasonCode) (int, bool) {
	switch code {
	case ReasonPersonSchema:
		return weightPersonSchema, true
	case ReasonHCard:
		return weightHCard, true
	case ReasonRelMe:
		return weightRelMe, true
	case ReasonFeed:
		return weightFeed, true
	case ReasonAboutLink:
		return weightAboutLink, true
	case ReasonNowLink:
		return weightNowLink, true
	case ReasonUsesLink:
		return weightUsesLink, true
	case ReasonProjectsLink:
		return weightProjectsLink, true
	case ReasonBlogLink:
		return weightBlogLink, true
	case ReasonAuthorMetadata:
		return weightAuthorMetadata, true
	case ReasonOrganizationSchema:
		return weightOrganizationSchema, true
	case ReasonProductSchema:
		return weightProductSchema, true
	case ReasonShoppingCart:
		return weightShoppingCart, true
	case ReasonCareersLink:
		return weightCareersLink, true
	case ReasonInvestorLink:
		return weightInvestorLink, true
	case ReasonMarketplace:
		return weightMarketplaceSignals, true
	case ReasonParkedDomain:
		return weightParkedDomain, true
	default:
		return 0, false
	}
}

// ContributionsFromReasons derives per-reason weights from scoring constants.
//
// Unknown codes are omitted. score_clamped is true when the sum of known
// weights differs from the persisted calculated score after clamping.
//
// @param reasons - Persisted reason codes from an assessed row.
// @param calculatedScore - Persisted clamped score.
// @returns Contributions and whether clamping changed the sum.
func ContributionsFromReasons(
	reasons []ReasonCode,
	calculatedScore int,
) ([]ScoreContribution, bool) {
	contributions := make([]ScoreContribution, 0, len(reasons))
	total := 0
	for _, code := range reasons {
		weight, ok := ReasonWeight(code)
		if !ok {
			continue
		}
		contributions = append(contributions, ScoreContribution{
			Code:   code,
			Weight: weight,
		})
		total += weight
	}
	return contributions, clampScore(total) != calculatedScore || total != calculatedScore
}

// ScoreBucket returns the private metrics bucket for a score in 0..100.
//
// @param score - Assessment score.
// @returns Bucket label such as "0-19".
func ScoreBucket(score int) string {
	switch {
	case score < 20:
		return "0-19"
	case score < 40:
		return "20-39"
	case score < 60:
		return "40-59"
	case score < 80:
		return "60-79"
	default:
		return "80-100"
	}
}
