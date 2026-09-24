// Package reporting exposes JoshBot's private operational state through a
// stable, versioned HTTP reporting contract.
package reporting

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

var (
	errReaderUnavailable = errors.New(
		"reporting: reader is unavailable",
	)
	errTokenUnavailable = errors.New(
		"reporting: authentication token is unavailable",
	)
	errClockUnavailable = errors.New(
		"reporting: clock is unavailable",
	)
	errInvalidLimit = errors.New(
		"reporting: invalid limit",
	)
)

// ControlStatus is the persistent operator-controlled processor state.
type ControlStatus struct {
	DiscoveryPaused    bool      `json:"discovery_paused"`
	VerificationPaused bool      `json:"verification_paused"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// BackpressureStatus describes the bounded automatic-discovery queue state.
//
// Active means automatic discovery is currently prevented from creating more
// probe work because PendingProbes has reached MaxPendingProbes.
type BackpressureStatus struct {
	AutomaticCrawlEnabled bool  `json:"automatic_crawl_enabled"`
	Active                bool  `json:"active"`
	PendingProbes         int64 `json:"pending_probes"`
	MaxPendingProbes      int64 `json:"max_pending_probes"`
}

// QueueSummary is a point-in-time verification queue summary.
type QueueSummary struct {
	Total             int64      `json:"total"`
	Probe             int64      `json:"probe"`
	Recurring         int64      `json:"recurring"`
	Leased            int64      `json:"leased"`
	OldestAvailableAt *time.Time `json:"oldest_available_at,omitempty"`
}

// SourceSummary is a point-in-time crawl-source classification summary.
type SourceSummary struct {
	Total         int64 `json:"total"`
	Seeded        int64 `json:"seeded"`
	Automatic     int64 `json:"automatic"`
	Verified      int64 `json:"verified"`
	Blocked       int64 `json:"blocked"`
	CrawlEligible int64 `json:"crawl_eligible"`
}

// ServiceStatus is the latest heartbeat from a JoshBot service instance.
type ServiceStatus struct {
	Service       string    `json:"service"`
	InstanceID    string    `json:"instance_id"`
	State         string    `json:"state"`
	CurrentOrigin string    `json:"current_origin,omitempty"`
	Message       string    `json:"message,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Status is the aggregate operational reporting snapshot.
type Status struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	Control      ControlStatus      `json:"control"`
	Backpressure BackpressureStatus `json:"backpressure"`
	Queue        QueueSummary       `json:"queue"`
	Sources      SourceSummary      `json:"sources"`
	Services     []ServiceStatus    `json:"services"`
}

// CrawlSource is one private crawl-source classification.
//
// These fields describe crawler operation only. They do not establish or imply
// Joshternet participation.
type CrawlSource struct {
	Origin                  string     `json:"origin"`
	Seeded                  bool       `json:"seeded"`
	AutomaticallyDiscovered bool       `json:"automatically_discovered"`
	Blocked                 bool       `json:"blocked"`
	Verified                bool       `json:"verified"`
	CrawlEligible           bool       `json:"crawl_eligible"`
	FirstDiscoveredAt       *time.Time `json:"first_discovered_at,omitempty"`
	LastDiscoveredAt        *time.Time `json:"last_discovered_at,omitempty"`
}

// CrawlRun is one durable bounded crawl execution.
type CrawlRun struct {
	ID                       int64      `json:"id"`
	SourceOrigin             string     `json:"source_origin"`
	StartedAt                time.Time  `json:"started_at"`
	FinishedAt               *time.Time `json:"finished_at,omitempty"`
	Outcome                  string     `json:"outcome"`
	StopReason               string     `json:"stop_reason"`
	PagesAttempted           int        `json:"pages_attempted"`
	PagesParsed              int        `json:"pages_parsed"`
	CandidatesDiscovered     int        `json:"candidates_discovered"`
	BudgetExhausted          bool       `json:"budget_exhausted"`
	MaxDepth                 int        `json:"max_depth"`
	MaxPages                 int        `json:"max_pages"`
	MaxPageBytes             int64      `json:"max_page_bytes"`
	RequestDelayMilliseconds int64      `json:"request_delay_milliseconds"`
	RedirectLimit            int        `json:"redirect_limit"`
	PageTimeoutMilliseconds  int64      `json:"page_timeout_milliseconds"`
	PromotionsAdmitted       int        `json:"promotions_admitted"`
	PromotionsDeferred       int        `json:"promotions_deferred"`
	FailureCategory          string     `json:"failure_category"`
	PagesBlocked             int        `json:"pages_blocked"`
	PagesFailed              int        `json:"pages_failed"`
	URLsFound                int        `json:"urls_found"`
	URLsEnqueued             int        `json:"urls_enqueued"`
	FrontierRemaining        int        `json:"frontier_remaining"`
}

// PageAttempt is one sanitized page request from a crawl run.
//
// RequestedURL and FinalURL are expected to be query- and fragment-free.
type PageAttempt struct {
	Sequence          int       `json:"sequence"`
	RequestedURL      string    `json:"requested_url"`
	FinalURL          string    `json:"final_url"`
	Depth             int       `json:"depth"`
	StartedAt         time.Time `json:"started_at"`
	DurationMillis    int64     `json:"duration_milliseconds"`
	StatusCode        *int      `json:"status_code,omitempty"`
	ResponseBytes     int64     `json:"response_bytes"`
	ContentType       string    `json:"content_type"`
	RedirectCount     int       `json:"redirect_count"`
	RobotsDecision    string    `json:"robots_decision"`
	InternalLinkCount int       `json:"internal_link_count"`
	ExternalLinkCount int       `json:"external_link_count"`
	Outcome           string    `json:"outcome"`
	FailureCategory   string    `json:"failure_category"`
	URLsFound         int       `json:"urls_found"`
	URLsEnqueued      int       `json:"urls_enqueued"`
}

// CrawlDetail is a run together with its ordered page attempts.
type CrawlDetail struct {
	Run   CrawlRun      `json:"run"`
	Pages []PageAttempt `json:"pages"`
}

// QueueItem is one current verification queue entry.
type QueueItem struct {
	Origin          string     `json:"origin"`
	Mode            string     `json:"mode"`
	AvailableAt     time.Time  `json:"available_at"`
	LeaseGeneration int64      `json:"lease_generation"`
	LeaseOwner      string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	LastClaimedAt   *time.Time `json:"last_claimed_at,omitempty"`
}

// QueueEvent is one durable verification queue transition.
type QueueEvent struct {
	ID              int64      `json:"id"`
	Origin          string     `json:"origin"`
	OccurredAt      time.Time  `json:"occurred_at"`
	Event           string     `json:"event"`
	Mode            string     `json:"mode"`
	AvailableAt     *time.Time `json:"available_at,omitempty"`
	LeaseOwner      string     `json:"lease_owner,omitempty"`
	LeaseGeneration int64      `json:"lease_generation"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
}

// AuditEvent is one immutable operator mutation attempt.
type AuditEvent struct {
	ID         int64     `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
	Caller     string    `json:"caller"`
	Actor      string    `json:"actor"`
	Result     string    `json:"result"`
	Reason     string    `json:"reason"`
}

// Reader is the reporting read model.
type Reader interface {
	Status(context.Context) (Status, error)

	Sources(
		context.Context,
		int,
	) ([]CrawlSource, error)

	Source(
		context.Context,
		string,
		int,
	) (SourceDetail, bool, error)

	Crawls(
		context.Context,
		int,
	) ([]CrawlRun, error)

	Crawl(
		context.Context,
		int64,
	) (CrawlDetail, bool, error)

	Queue(
		context.Context,
		int,
	) ([]QueueItem, error)

	QueueEvents(
		context.Context,
		int,
	) ([]QueueEvent, error)

	Audits(
		context.Context,
		int,
	) ([]AuditEvent, error)

	Services(
		context.Context,
	) ([]ServiceStatus, error)
}

type handler struct {
	reader                    Reader
	expectedAuthorizationHash [sha256.Size]byte
	now                       func() time.Time
}

// NewHandler constructs the private reporting HTTP handler.
func NewHandler(
	reader Reader,
	token string,
) (http.Handler, error) {
	return newHandler(
		reader,
		token,
		time.Now,
	)
}

func newHandler(
	reader Reader,
	token string,
	now func() time.Time,
) (http.Handler, error) {
	if reader == nil {
		return nil, errReaderUnavailable
	}

	if strings.TrimSpace(token) == "" {
		return nil, errTokenUnavailable
	}

	if now == nil {
		return nil, errClockUnavailable
	}

	runtime := &handler{
		reader: reader,
		expectedAuthorizationHash: reportingAuthorizationDigest(
			"Bearer " + token,
		),
		now: now,
	}

	mux := http.NewServeMux()

	mux.HandleFunc(
		"GET /healthz",
		runtime.health,
	)

	mux.Handle(
		"GET /api/v1/status",
		runtime.authorize(
			http.HandlerFunc(runtime.status),
		),
	)

	mux.Handle(
		"GET /api/v1/sources",
		runtime.authorize(
			http.HandlerFunc(runtime.sources),
		),
	)

	mux.Handle(
		"GET /api/v1/sources/detail",
		runtime.authorize(
			http.HandlerFunc(runtime.sourceDetail),
		),
	)

	mux.Handle(
		"GET /api/v1/crawls",
		runtime.authorize(
			http.HandlerFunc(runtime.crawls),
		),
	)

	mux.Handle(
		"GET /api/v1/crawls/{id}",
		runtime.authorize(
			http.HandlerFunc(runtime.crawl),
		),
	)

	mux.Handle(
		"GET /api/v1/queue",
		runtime.authorize(
			http.HandlerFunc(runtime.queue),
		),
	)

	mux.Handle(
		"GET /api/v1/queue/events",
		runtime.authorize(
			http.HandlerFunc(runtime.queueEvents),
		),
	)

	mux.Handle(
		"GET /api/v1/services",
		runtime.authorize(
			http.HandlerFunc(runtime.services),
		),
	)

	mux.Handle(
		"GET /api/v1/audit",
		runtime.authorize(
			http.HandlerFunc(runtime.audits),
		),
	)

	mux.Handle(
		"GET /metrics",
		runtime.authorize(
			http.HandlerFunc(runtime.metrics),
		),
	)

	return runtime.withHeaders(
		runtime.withPagination(mux),
	), nil
}

func (h *handler) withHeaders(
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(
		func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			writer.Header().Set(
				"Cache-Control",
				"no-store",
			)
			writer.Header().Set(
				"X-Content-Type-Options",
				"nosniff",
			)

			next.ServeHTTP(writer, request)
		},
	)
}

func (h *handler) authorize(
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(
		func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if !reportingAuthorizationMatches(
				h.expectedAuthorizationHash,
				request.Header.Get("Authorization"),
			) {
				writer.Header().Set(
					"WWW-Authenticate",
					`Bearer realm="joshbot-reporting"`,
				)

				writeError(
					writer,
					http.StatusUnauthorized,
					"unauthorized",
				)

				return
			}

			next.ServeHTTP(
				writer,
				request,
			)
		},
	)
}

func reportingAuthorizationDigest(value string) [sha256.Size]byte {
	return sha256.Sum256([]byte(value))
}

func reportingAuthorizationMatches(
	expected [sha256.Size]byte,
	provided string,
) bool {
	providedHash := reportingAuthorizationDigest(provided)
	return subtle.ConstantTimeCompare(
		providedHash[:],
		expected[:],
	) == 1
}

func (h *handler) health(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	writeJSON(
		writer,
		http.StatusOK,
		map[string]string{
			"status": "ok",
		},
	)
}

func (h *handler) status(
	writer http.ResponseWriter,
	request *http.Request,
) {
	status, err := h.reader.Status(
		request.Context(),
	)
	if err != nil {
		writeInternalError(writer)

		return
	}
	status.GeneratedAt = h.now().UTC()

	writeJSON(
		writer,
		http.StatusOK,
		status,
	)
}

func (h *handler) sources(
	writer http.ResponseWriter,
	request *http.Request,
) {
	limit, err := requestLimit(request)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
		)

		return
	}

	sources, err := h.reader.Sources(
		request.Context(),
		limit,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		sources,
	)
}

func (h *handler) crawls(
	writer http.ResponseWriter,
	request *http.Request,
) {
	limit, err := requestLimit(request)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
		)

		return
	}

	runs, err := h.reader.Crawls(
		request.Context(),
		limit,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		runs,
	)
}

func (h *handler) crawl(
	writer http.ResponseWriter,
	request *http.Request,
) {
	id, err := strconv.ParseInt(
		request.PathValue("id"),
		10,
		64,
	)
	if err != nil || id <= 0 {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_crawl_id",
		)

		return
	}

	detail, found, err := h.reader.Crawl(
		request.Context(),
		id,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	if !found {
		writeError(
			writer,
			http.StatusNotFound,
			"crawl_not_found",
		)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		detail,
	)
}

func (h *handler) queue(
	writer http.ResponseWriter,
	request *http.Request,
) {
	limit, err := requestLimit(request)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
		)

		return
	}

	items, err := h.reader.Queue(
		request.Context(),
		limit,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		items,
	)
}

func (h *handler) queueEvents(
	writer http.ResponseWriter,
	request *http.Request,
) {
	limit, err := requestLimit(request)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
		)

		return
	}

	events, err := h.reader.QueueEvents(
		request.Context(),
		limit,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		events,
	)
}

func (h *handler) services(
	writer http.ResponseWriter,
	request *http.Request,
) {
	services, err := h.reader.Services(
		request.Context(),
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		services,
	)
}

func (h *handler) audits(
	writer http.ResponseWriter,
	request *http.Request,
) {
	limit, err := requestLimit(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_limit")
		return
	}
	events, err := h.reader.Audits(request.Context(), limit)
	if err != nil {
		writeInternalError(writer)
		return
	}
	writeJSON(writer, http.StatusOK, events)
}

func (h *handler) metrics(
	writer http.ResponseWriter,
	request *http.Request,
) {
	status, err := h.reader.Status(
		request.Context(),
	)
	if err != nil {
		writeInternalError(writer)

		return
	}
	var durable OperationalMetrics
	if reader, ok := h.reader.(metricsReader); ok {
		durable, err = reader.Metrics(request.Context())
		if err != nil {
			writeInternalError(writer)
			return
		}
	}

	now := h.now().UTC()

	writer.Header().Set(
		"Content-Type",
		"application/openmetrics-text; version=1.0.0; charset=utf-8",
	)
	writer.WriteHeader(http.StatusOK)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_discovery_paused Whether discovery is administratively paused.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_discovery_paused gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_discovery_paused %d\n",
		metricBool(status.Control.DiscoveryPaused),
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_verification_paused Whether verification is administratively paused.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_verification_paused gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_verification_paused %d\n",
		metricBool(
			status.Control.VerificationPaused,
		),
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_automatic_crawl_enabled Whether automatic crawl-source promotion is enabled.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_automatic_crawl_enabled gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_automatic_crawl_enabled %d\n",
		metricBool(
			status.Backpressure.
				AutomaticCrawlEnabled,
		),
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_backpressure_active Whether automatic discovery is currently backpressured.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_backpressure_active gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_backpressure_active %d\n",
		metricBool(
			status.Backpressure.Active,
		),
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_pending_probes Current verification probe backlog.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_pending_probes gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_pending_probes %d\n",
		status.Backpressure.PendingProbes,
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_pending_probe_limit Configured automatic-discovery backpressure limit.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_pending_probe_limit gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_pending_probe_limit %d\n",
		status.Backpressure.MaxPendingProbes,
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_verification_queue Current verification queue entries.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_verification_queue gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_verification_queue{mode=\"all\"} %d\n",
		status.Queue.Total,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_verification_queue{mode=\"probe\"} %d\n",
		status.Queue.Probe,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_verification_queue{mode=\"recurring\"} %d\n",
		status.Queue.Recurring,
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_verification_leases Current active verification leases.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_verification_leases gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_verification_leases %d\n",
		status.Queue.Leased,
	)

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_crawl_sources Private crawl-source counts by classification.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_crawl_sources gauge",
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_crawl_sources{classification=\"all\"} %d\n",
		status.Sources.Total,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_crawl_sources{classification=\"seeded\"} %d\n",
		status.Sources.Seeded,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_crawl_sources{classification=\"automatic\"} %d\n",
		status.Sources.Automatic,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_crawl_sources{classification=\"verified\"} %d\n",
		status.Sources.Verified,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_crawl_sources{classification=\"blocked\"} %d\n",
		status.Sources.Blocked,
	)
	_, _ = fmt.Fprintf(
		writer,
		"joshbot_crawl_sources{classification=\"crawl_eligible\"} %d\n",
		status.Sources.CrawlEligible,
	)

	durableMetrics := []struct {
		name  string
		help  string
		value int64
	}{
		{"joshbot_retained_candidates", "Retained discovery candidates.", durable.Candidates},
		{"joshbot_retained_discovery_edges", "Retained discovery edges.", durable.DiscoveryEdges},
		{"joshbot_retained_crawl_runs", "Retained crawl runs.", durable.CrawlRuns},
		{"joshbot_unfinished_crawl_runs", "Current unfinished crawl runs.", durable.UnfinishedCrawlRuns},
		{"joshbot_retained_pages_attempted", "Retained pages attempted.", durable.PagesAttempted},
		{"joshbot_retained_pages_parsed", "Retained pages parsed.", durable.PagesParsed},
		{"joshbot_retained_origins_found", "Retained origins found by crawls.", durable.OriginsFound},
		{"joshbot_retained_origins_promoted", "Retained origins admitted automatically.", durable.OriginsPromoted},
		{"joshbot_retained_origins_deferred", "Retained non-admitted original link candidates.", durable.OriginsDeferred},
		{"joshbot_retained_failures", "Retained failed page attempts.", durable.Failures},
		{"joshbot_retained_robots_denials", "Retained robots denials.", durable.RobotsDenials},
	}
	for _, metric := range durableMetrics {
		_, _ = fmt.Fprintf(writer, "# HELP %s %s\n", metric.name, metric.help)
		_, _ = fmt.Fprintf(writer, "# TYPE %s gauge\n", metric.name)
		_, _ = fmt.Fprintf(writer, "%s %d\n", metric.name, metric.value)
	}

	_, _ = fmt.Fprintln(
		writer,
		"# HELP joshbot_service_heartbeat_age_seconds Age of the latest service heartbeat.",
	)
	_, _ = fmt.Fprintln(
		writer,
		"# TYPE joshbot_service_heartbeat_age_seconds gauge",
	)

	latestServices := make(
		map[string]ServiceStatus,
		len(status.Services),
	)
	serviceOrder := make(
		[]string,
		0,
		len(status.Services),
	)

	for _, service := range status.Services {
		current, exists := latestServices[service.Service]
		if !exists {
			serviceOrder = append(
				serviceOrder,
				service.Service,
			)
		}

		if !exists ||
			service.UpdatedAt.After(
				current.UpdatedAt,
			) {
			latestServices[service.Service] = service
		}
	}

	for _, serviceName := range serviceOrder {
		service := latestServices[serviceName]

		age := now.Sub(
			service.UpdatedAt.UTC(),
		).Seconds()

		if age < 0 {
			age = 0
		}

		_, _ = fmt.Fprintf(
			writer,
			"joshbot_service_heartbeat_age_seconds{service=\"%s\",state=\"%s\"} %g\n",
			metricLabel(service.Service),
			metricLabel(service.State),
			age,
		)
	}

	_, _ = fmt.Fprintln(
		writer,
		"# EOF",
	)
}

func requestLimit(
	request *http.Request,
) (int, error) {
	raw := request.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil ||
		limit < 1 ||
		limit > maxLimit {
		return 0, errInvalidLimit
	}

	return limit, nil
}

func metricBool(
	value bool,
) int {
	if value {
		return 1
	}

	return 0
}

func metricLabel(
	value string,
) string {
	replacer := strings.NewReplacer(
		`\`,
		`\\`,
		"\n",
		`\n`,
		`"`,
		`\"`,
	)

	return replacer.Replace(value)
}

func writeInternalError(
	writer http.ResponseWriter,
) {
	writeError(
		writer,
		http.StatusInternalServerError,
		"internal_error",
	)
}

func writeError(
	writer http.ResponseWriter,
	status int,
	code string,
) {
	writeJSON(
		writer,
		status,
		map[string]string{
			"error": code,
		},
	)
}

func writeJSON(
	writer http.ResponseWriter,
	status int,
	value any,
) {
	writer.Header().Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)
	writer.WriteHeader(status)

	_ = json.NewEncoder(writer).Encode(value)
}
