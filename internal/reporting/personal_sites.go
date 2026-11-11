package reporting

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/joshternet/joshbot/internal/personalsite"
)

// ScoreContribution is one reason-code weight from scoring constants.
type ScoreContribution struct {
	Code   string `json:"code"`
	Weight int    `json:"weight"`
}

// PersonalSite is one discovery candidate's personal-site assessment view.
type PersonalSite struct {
	Origin               string              `json:"origin"`
	QualificationOutcome *string             `json:"qualification_outcome"`
	CalculatedScore      *int                `json:"calculated_score"`
	EffectiveScore       *int                `json:"effective_score"`
	AssessmentVersion    *int                `json:"assessment_version"`
	AssessedAt           *time.Time          `json:"assessed_at"`
	Truncated            bool                `json:"truncated"`
	ReasonCodes          []string            `json:"reason_codes"`
	ScoreContributions   []ScoreContribution `json:"score_contributions"`
	ScoreClamped         bool                `json:"score_clamped"`
	NextAttemptAt        *time.Time          `json:"next_attempt_at"`
	LastAttemptAt        *time.Time          `json:"last_attempt_at"`
	FailureCount         int                 `json:"failure_count"`
	HTTPStatus           *int                `json:"http_status"`
	RetryClass           *string             `json:"retry_class"`
	OverrideScore        *int                `json:"override_score"`
	OverrideReason       *string             `json:"override_reason"`
	OverrideActor        *string             `json:"override_actor"`
	OverrideUpdatedAt    *time.Time          `json:"override_updated_at"`
	OverrideRevision     int64               `json:"override_revision"`
	AdmissionEligible    bool                `json:"admission_eligible"`
	AdmissionState       string              `json:"admission_state"`
	CrawlState           string              `json:"crawl_state"`
	RescorePending       bool                `json:"rescore_pending"`
}

type personalSiteCursor struct {
	EffectiveScore *int
	Origin         string
	HasScore       bool
}

type personalSiteQuery struct {
	Limit    int
	Cursor   *personalSiteCursor
	Origin   string
	Outcome  string
	Awaiting *bool
}

type personalSitePage struct {
	Items      []PersonalSite
	NextCursor string
}

type personalSiteReader interface {
	PersonalSitesPage(
		context.Context,
		personalSiteQuery,
	) (personalSitePage, error)

	PersonalSite(
		context.Context,
		string,
	) (PersonalSite, bool, error)
}

// BuildInfo identifies one process binary.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	State   string `json:"state"`
}

// BuildStatus reports reporter and fleet build identities separately.
type BuildStatus struct {
	Reporter BuildInfo       `json:"reporter"`
	Services []ServiceStatus `json:"services"`
}

func enrichPersonalSite(site *PersonalSite) {
	if site.ReasonCodes == nil {
		site.ReasonCodes = []string{}
	}
	if site.ScoreContributions == nil {
		site.ScoreContributions = []ScoreContribution{}
	}
	if site.CalculatedScore == nil || len(site.ReasonCodes) == 0 {
		return
	}
	reasons := make([]personalsite.ReasonCode, 0, len(site.ReasonCodes))
	for _, code := range site.ReasonCodes {
		reasons = append(reasons, personalsite.ReasonCode(code))
	}
	contributions, clamped := personalsite.ContributionsFromReasons(
		reasons,
		*site.CalculatedScore,
	)
	site.ScoreClamped = clamped
	site.ScoreContributions = make([]ScoreContribution, 0, len(contributions))
	for _, item := range contributions {
		site.ScoreContributions = append(
			site.ScoreContributions,
			ScoreContribution{
				Code:   string(item.Code),
				Weight: item.Weight,
			},
		)
	}
}

func (h *handler) personalSites(
	writer http.ResponseWriter,
	request *http.Request,
) {
	reader, ok := h.reader.(personalSiteReader)
	if !ok {
		writeInternalError(writer)
		return
	}
	query, err := parsePersonalSiteQuery(request)
	if err != nil {
		writeQueryError(writer, err)
		return
	}
	page, err := reader.PersonalSitesPage(request.Context(), query)
	if err != nil {
		writeInternalError(writer)
		return
	}
	writePage(writer, page.NextCursor, page.Items)
}

func (h *handler) personalSiteDetail(
	writer http.ResponseWriter,
	request *http.Request,
) {
	sourceOrigin, err := requestSourceOrigin(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_origin")
		return
	}
	reader, ok := h.reader.(personalSiteReader)
	if !ok {
		writeInternalError(writer)
		return
	}
	site, found, err := reader.PersonalSite(request.Context(), sourceOrigin)
	if err != nil {
		if errors.Is(err, errInvalidOriginFilter) {
			writeError(writer, http.StatusBadRequest, "invalid_origin")
			return
		}
		writeInternalError(writer)
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(writer, http.StatusOK, site)
}

func (h *handler) buildIdentity(
	writer http.ResponseWriter,
	request *http.Request,
) {
	services, err := h.reader.Services(request.Context())
	if err != nil {
		writeInternalError(writer)
		return
	}
	writeJSON(writer, http.StatusOK, BuildStatus{
		Reporter: h.build,
		Services: services,
	})
}

func parsePersonalSiteQuery(
	request *http.Request,
) (personalSiteQuery, error) {
	limit, err := requestLimit(request)
	if err != nil {
		return personalSiteQuery{}, err
	}
	values := request.URL.Query()
	originFilter, err := queryOrigin(values)
	if err != nil {
		return personalSiteQuery{}, err
	}
	outcome, err := queryEnum(
		values,
		"outcome",
		map[string]struct{}{
			"assessed":            {},
			"robots_denied":       {},
			"timeout":             {},
			"unsupported_content": {},
			"too_large":           {},
			"dns_failure":         {},
			"http_error":          {},
			"network_rejected":    {},
			"parser_failure":      {},
			"unavailable":         {},
		},
	)
	if err != nil {
		return personalSiteQuery{}, err
	}
	awaiting, err := queryBool(values, "awaiting")
	if err != nil {
		return personalSiteQuery{}, err
	}
	cursor, err := decodePersonalSiteCursor(queryValue(values, "cursor"))
	if err != nil {
		return personalSiteQuery{}, err
	}
	return personalSiteQuery{
		Limit:    limit,
		Cursor:   cursor,
		Origin:   originFilter,
		Outcome:  outcome,
		Awaiting: awaiting,
	}, nil
}

func decodePersonalSiteCursor(
	raw string,
) (*personalSiteCursor, error) {
	if raw == "" {
		return nil, nil
	}
	payload, err := decodeCursor(raw, "personal_sites")
	if err != nil {
		return nil, err
	}
	cursor := &personalSiteCursor{Origin: payload.Origin}
	switch payload.At {
	case "score":
		score := int(payload.ID)
		cursor.EffectiveScore = &score
		cursor.HasScore = true
	case "null":
		cursor.HasScore = false
	default:
		return nil, errInvalidCursor
	}
	if cursor.Origin == "" {
		return nil, errInvalidCursor
	}
	return cursor, nil
}

func encodePersonalSiteCursor(site PersonalSite) string {
	payload := cursorPayload{
		Version: cursorVersion,
		Kind:    "personal_sites",
		Origin:  site.Origin,
	}
	if site.EffectiveScore != nil {
		payload.At = "score"
		payload.ID = int64(*site.EffectiveScore)
	} else {
		payload.At = "null"
	}
	return encodeCursor(payload)
}
