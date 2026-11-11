// Goal & Constraints: Define bounded structural signals and assessment
// types for personal-site crawl prioritization only. No identity, trust,
// participation, or content-quality semantics. Inputs are booleans only;
// outputs are a 0-100 score plus enum-like reason codes with no page text.
package personalsite

// AssessmentVersion identifies the current scoring behavior.
//
// Increment when weights or reason codes materially change.
const AssessmentVersion = 1

// ExtractStatus describes whether signal extraction completed.
type ExtractStatus uint8

const (
	// ExtractStatusComplete means signals were derived from HTML.
	ExtractStatusComplete ExtractStatus = iota + 1
	// ExtractStatusUnsupportedContent means the body was not HTML.
	ExtractStatusUnsupportedContent
	// ExtractStatusTooLarge means size limits rejected the body.
	ExtractStatusTooLarge
	// ExtractStatusUnavailable means decode or parse failed.
	ExtractStatusUnavailable
)

// SiteSignals is a bounded set of structural booleans from one root page.
//
// Fields must remain free of names, titles, biographies, and page text.
type SiteSignals struct {
	HasPersonSchema       bool
	HasOrganizationSchema bool
	HasHCard              bool
	HasRelMe              bool
	HasFeed               bool
	HasAboutLink          bool
	HasNowLink            bool
	HasUsesLink           bool
	HasProjectsLink       bool
	HasBlogLink           bool
	HasAuthorMetadata     bool

	HasProductSchema      bool
	HasShoppingCart       bool
	HasCareersLink        bool
	HasInvestorLink       bool
	HasMarketplaceSignals bool
	HasParkedDomain       bool
}

// ReasonCode is a bounded explanation token for a score contribution.
type ReasonCode string

const (
	ReasonPersonSchema       ReasonCode = "person_schema"
	ReasonHCard              ReasonCode = "h_card"
	ReasonRelMe              ReasonCode = "rel_me"
	ReasonFeed               ReasonCode = "feed"
	ReasonAboutLink          ReasonCode = "about_link"
	ReasonNowLink            ReasonCode = "now_link"
	ReasonUsesLink           ReasonCode = "uses_link"
	ReasonProjectsLink       ReasonCode = "projects_link"
	ReasonBlogLink           ReasonCode = "blog_link"
	ReasonAuthorMetadata     ReasonCode = "author_metadata"
	ReasonOrganizationSchema ReasonCode = "organization_schema"
	ReasonProductSchema      ReasonCode = "product_schema"
	ReasonShoppingCart       ReasonCode = "shopping_cart"
	ReasonCareersLink        ReasonCode = "careers_link"
	ReasonInvestorLink       ReasonCode = "investor_link"
	ReasonMarketplace        ReasonCode = "marketplace"
	ReasonParkedDomain       ReasonCode = "parked_domain"
)

// Assessment is the deterministic personal-site priority result.
type Assessment struct {
	Score   int
	Reasons []ReasonCode
	Version int
}

// QualificationOutcome describes a qualification attempt result.
type QualificationOutcome string

const (
	// QualificationAssessed means a score was produced.
	QualificationAssessed QualificationOutcome = "assessed"
	// QualificationRobotsDenied means robots forbade the root page.
	QualificationRobotsDenied QualificationOutcome = "robots_denied"
	// QualificationTimeout means the root fetch timed out.
	QualificationTimeout QualificationOutcome = "timeout"
	// QualificationUnsupportedContent means the root was not HTML.
	QualificationUnsupportedContent QualificationOutcome = "unsupported_content"
	// QualificationTooLarge means the root exceeded size limits.
	QualificationTooLarge QualificationOutcome = "too_large"
	// QualificationDNSFailure means DNS resolution failed.
	QualificationDNSFailure QualificationOutcome = "dns_failure"
	// QualificationHTTPError means a non-success HTTP status.
	QualificationHTTPError QualificationOutcome = "http_error"
	// QualificationNetworkRejected means netguard rejected the destination.
	QualificationNetworkRejected QualificationOutcome = "network_rejected"
	// QualificationParserFailure means HTML parsing failed.
	QualificationParserFailure QualificationOutcome = "parser_failure"
	// QualificationUnavailable means a generic fetch failure.
	QualificationUnavailable QualificationOutcome = "unavailable"
)

// Qualification is the private result of one root-page inspection.
type Qualification struct {
	Outcome    QualificationOutcome
	Assessment Assessment
}
