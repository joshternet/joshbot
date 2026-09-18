package reporting

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

var errInvalidSourceOrigin = errors.New(
	"reporting: source origin is invalid",
)

// DiscoveryProvenance identifies one origin that discovered this source.
type DiscoveryProvenance struct {
	SourceOrigin      string    `json:"source_origin"`
	Kind              string    `json:"kind"`
	FirstDiscoveredAt time.Time `json:"first_discovered_at"`
	LastDiscoveredAt  time.Time `json:"last_discovered_at"`
}

// RobotsObservation is the most recent stored robots decision from a crawl.
type RobotsObservation struct {
	CrawlRunID int64     `json:"crawl_run_id"`
	Sequence   int       `json:"sequence"`
	ObservedAt time.Time `json:"observed_at"`
	Decision   string    `json:"decision"`
}

// SourceDetail is the operational view of one private crawl source.
type SourceDetail struct {
	Source                  CrawlSource           `json:"source"`
	DiscoveredBy            []DiscoveryProvenance `json:"discovered_by"`
	Queue                   *QueueItem            `json:"queue,omitempty"`
	LatestCrawl             *CrawlRun             `json:"latest_crawl,omitempty"`
	LatestRobotsObservation *RobotsObservation    `json:"latest_robots_observation,omitempty"`
}

func (h *handler) sourceDetail(
	writer http.ResponseWriter,
	request *http.Request,
) {
	sourceOrigin, err := requestSourceOrigin(request)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_origin",
		)

		return
	}

	limit, err := requestLimit(request)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
		)

		return
	}

	detail, found, err := h.reader.Source(
		request.Context(),
		sourceOrigin,
		limit,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	if !found {
		writeError(
			writer,
			http.StatusNotFound,
			"source_not_found",
		)

		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		detail,
	)
}

func requestSourceOrigin(
	request *http.Request,
) (string, error) {
	raw := strings.TrimSpace(
		request.URL.Query().Get("origin"),
	)
	if raw == "" {
		return "", errInvalidSourceOrigin
	}

	parsed, err := origin.Parse(raw)
	if err != nil || parsed.String() == "" {
		return "", errInvalidSourceOrigin
	}

	return parsed.String(), nil
}
