package reporting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

const (
	nextCursorHeader = "X-JoshBot-Next-Cursor"
	cursorVersion    = 1
)

var (
	errInvalidCursor = errors.New(
		"reporting: invalid cursor",
	)
	errInvalidFilter = errors.New(
		"reporting: invalid filter",
	)
	errInvalidOriginFilter = errors.New(
		"reporting: invalid origin filter",
	)
)

type sourceCursor struct {
	Origin string
}

type crawlCursor struct {
	StartedAt time.Time
	ID        int64
}

type queueCursor struct {
	AvailableAt time.Time
	Origin      string
}

type queueEventCursor struct {
	OccurredAt time.Time
	ID         int64
}

type auditCursor struct {
	OccurredAt time.Time
	ID         int64
}

type auditQuery struct {
	Limit  int
	Cursor *auditCursor
}

type auditPage struct {
	Items      []AuditEvent
	NextCursor string
}

type sourceQuery struct {
	Limit         int
	Cursor        *sourceCursor
	Origin        string
	Seeded        *bool
	Automatic     *bool
	Verified      *bool
	Blocked       *bool
	CrawlEligible *bool
}

type crawlQuery struct {
	Limit   int
	Cursor  *crawlCursor
	Origin  string
	Outcome string
}

type queueQuery struct {
	Limit  int
	Cursor *queueCursor
	Origin string
	Mode   string
	Leased *bool
}

type queueEventQuery struct {
	Limit  int
	Cursor *queueEventCursor
	Origin string
	Event  string
	Mode   string
}

type sourcePage struct {
	Items      []CrawlSource
	NextCursor string
}

type crawlPage struct {
	Items      []CrawlRun
	NextCursor string
}

type queuePage struct {
	Items      []QueueItem
	NextCursor string
}

type queueEventPage struct {
	Items      []QueueEvent
	NextCursor string
}

type pagedReader interface {
	SourcesPage(
		context.Context,
		sourceQuery,
	) (sourcePage, error)

	CrawlsPage(
		context.Context,
		crawlQuery,
	) (crawlPage, error)

	QueuePage(
		context.Context,
		queueQuery,
	) (queuePage, error)

	QueueEventsPage(
		context.Context,
		queueEventQuery,
	) (queueEventPage, error)

	AuditsPage(
		context.Context,
		auditQuery,
	) (auditPage, error)
}

type cursorPayload struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	Origin  string `json:"origin,omitempty"`
	At      string `json:"at,omitempty"`
	ID      int64  `json:"id,omitempty"`
}

func (h *handler) withPagination(
	next http.Handler,
) http.Handler {
	reader, ok := h.reader.(pagedReader)
	if !ok {
		return next
	}

	return http.HandlerFunc(
		func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.Method != http.MethodGet {
				next.ServeHTTP(
					writer,
					request,
				)

				return
			}

			var pageHandler http.HandlerFunc

			switch request.URL.Path {
			case "/api/v1/sources":
				pageHandler = func(
					writer http.ResponseWriter,
					request *http.Request,
				) {
					h.pagedSources(
						writer,
						request,
						reader,
					)
				}
			case "/api/v1/crawls":
				pageHandler = func(
					writer http.ResponseWriter,
					request *http.Request,
				) {
					h.pagedCrawls(
						writer,
						request,
						reader,
					)
				}
			case "/api/v1/queue":
				pageHandler = func(
					writer http.ResponseWriter,
					request *http.Request,
				) {
					h.pagedQueue(
						writer,
						request,
						reader,
					)
				}
			case "/api/v1/queue/events":
				pageHandler = func(
					writer http.ResponseWriter,
					request *http.Request,
				) {
					h.pagedQueueEvents(
						writer,
						request,
						reader,
					)
				}
			case "/api/v1/audit":
				pageHandler = func(
					writer http.ResponseWriter,
					request *http.Request,
				) {
					h.pagedAudits(writer, request, reader)
				}
			default:
				next.ServeHTTP(
					writer,
					request,
				)

				return
			}

			h.authorize(pageHandler).ServeHTTP(
				writer,
				request,
			)
		},
	)
}

func (h *handler) pagedSources(
	writer http.ResponseWriter,
	request *http.Request,
	reader pagedReader,
) {
	query, err := parseSourceQuery(request)
	if err != nil {
		writeQueryError(writer, err)

		return
	}

	page, err := reader.SourcesPage(
		request.Context(),
		query,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writePage(
		writer,
		page.NextCursor,
		page.Items,
	)
}

func (h *handler) pagedCrawls(
	writer http.ResponseWriter,
	request *http.Request,
	reader pagedReader,
) {
	query, err := parseCrawlQuery(request)
	if err != nil {
		writeQueryError(writer, err)

		return
	}

	page, err := reader.CrawlsPage(
		request.Context(),
		query,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writePage(
		writer,
		page.NextCursor,
		page.Items,
	)
}

func (h *handler) pagedQueue(
	writer http.ResponseWriter,
	request *http.Request,
	reader pagedReader,
) {
	query, err := parseQueueQuery(request)
	if err != nil {
		writeQueryError(writer, err)

		return
	}

	page, err := reader.QueuePage(
		request.Context(),
		query,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writePage(
		writer,
		page.NextCursor,
		page.Items,
	)
}

func (h *handler) pagedQueueEvents(
	writer http.ResponseWriter,
	request *http.Request,
	reader pagedReader,
) {
	query, err := parseQueueEventQuery(request)
	if err != nil {
		writeQueryError(writer, err)

		return
	}

	page, err := reader.QueueEventsPage(
		request.Context(),
		query,
	)
	if err != nil {
		writeInternalError(writer)

		return
	}

	writePage(
		writer,
		page.NextCursor,
		page.Items,
	)
}

func (h *handler) pagedAudits(
	writer http.ResponseWriter,
	request *http.Request,
	reader pagedReader,
) {
	limit, err := requestLimit(request)
	if err != nil {
		writeQueryError(writer, err)
		return
	}
	cursor, err := decodeAuditCursor(
		queryValue(request.URL.Query(), "cursor"),
	)
	if err != nil {
		writeQueryError(writer, err)
		return
	}
	page, err := reader.AuditsPage(
		request.Context(),
		auditQuery{Limit: limit, Cursor: cursor},
	)
	if err != nil {
		writeInternalError(writer)
		return
	}
	writePage(writer, page.NextCursor, page.Items)
}

func parseSourceQuery(
	request *http.Request,
) (sourceQuery, error) {
	limit, err := requestLimit(request)
	if err != nil {
		return sourceQuery{}, err
	}

	values := request.URL.Query()
	originFilter, err := queryOrigin(values)
	if err != nil {
		return sourceQuery{}, err
	}

	seeded, err := queryBool(values, "seeded")
	if err != nil {
		return sourceQuery{}, err
	}

	automatic, err := queryBool(values, "automatic")
	if err != nil {
		return sourceQuery{}, err
	}

	verified, err := queryBool(values, "verified")
	if err != nil {
		return sourceQuery{}, err
	}

	blocked, err := queryBool(values, "blocked")
	if err != nil {
		return sourceQuery{}, err
	}

	crawlEligible, err := queryBool(
		values,
		"crawl_eligible",
	)
	if err != nil {
		return sourceQuery{}, err
	}

	cursor, err := decodeSourceCursor(
		queryValue(values, "cursor"),
	)
	if err != nil {
		return sourceQuery{}, err
	}

	return sourceQuery{
		Limit:         limit,
		Cursor:        cursor,
		Origin:        originFilter,
		Seeded:        seeded,
		Automatic:     automatic,
		Verified:      verified,
		Blocked:       blocked,
		CrawlEligible: crawlEligible,
	}, nil
}

func parseCrawlQuery(
	request *http.Request,
) (crawlQuery, error) {
	limit, err := requestLimit(request)
	if err != nil {
		return crawlQuery{}, err
	}

	values := request.URL.Query()
	originFilter, err := queryOrigin(values)
	if err != nil {
		return crawlQuery{}, err
	}

	outcome, err := queryEnum(
		values,
		"outcome",
		map[string]struct{}{
			"running":          {},
			"complete":         {},
			"budget_exhausted": {},
			"canceled":         {},
			"failed":           {},
		},
	)
	if err != nil {
		return crawlQuery{}, err
	}

	cursor, err := decodeCrawlCursor(
		queryValue(values, "cursor"),
	)
	if err != nil {
		return crawlQuery{}, err
	}

	return crawlQuery{
		Limit:   limit,
		Cursor:  cursor,
		Origin:  originFilter,
		Outcome: outcome,
	}, nil
}

func parseQueueQuery(
	request *http.Request,
) (queueQuery, error) {
	limit, err := requestLimit(request)
	if err != nil {
		return queueQuery{}, err
	}

	values := request.URL.Query()
	originFilter, err := queryOrigin(values)
	if err != nil {
		return queueQuery{}, err
	}

	mode, err := queryEnum(
		values,
		"mode",
		map[string]struct{}{
			"probe":     {},
			"reprobe":   {},
			"recurring": {},
		},
	)
	if err != nil {
		return queueQuery{}, err
	}

	leased, err := queryBool(values, "leased")
	if err != nil {
		return queueQuery{}, err
	}

	cursor, err := decodeQueueCursor(
		queryValue(values, "cursor"),
	)
	if err != nil {
		return queueQuery{}, err
	}

	return queueQuery{
		Limit:  limit,
		Cursor: cursor,
		Origin: originFilter,
		Mode:   mode,
		Leased: leased,
	}, nil
}

func parseQueueEventQuery(
	request *http.Request,
) (queueEventQuery, error) {
	limit, err := requestLimit(request)
	if err != nil {
		return queueEventQuery{}, err
	}

	values := request.URL.Query()
	originFilter, err := queryOrigin(values)
	if err != nil {
		return queueEventQuery{}, err
	}

	event, err := queryEnum(
		values,
		"event",
		map[string]struct{}{
			"scheduled":   {},
			"claimed":     {},
			"renewed":     {},
			"rescheduled": {},
			"completed":   {},
			"removed":     {},
		},
	)
	if err != nil {
		return queueEventQuery{}, err
	}

	mode, err := queryEnum(
		values,
		"mode",
		map[string]struct{}{
			"probe":     {},
			"reprobe":   {},
			"recurring": {},
		},
	)
	if err != nil {
		return queueEventQuery{}, err
	}

	cursor, err := decodeQueueEventCursor(
		queryValue(values, "cursor"),
	)
	if err != nil {
		return queueEventQuery{}, err
	}

	return queueEventQuery{
		Limit:  limit,
		Cursor: cursor,
		Origin: originFilter,
		Event:  event,
		Mode:   mode,
	}, nil
}

func queryValue(
	values url.Values,
	name string,
) string {
	items, exists := values[name]
	if !exists || len(items) == 0 {
		return ""
	}

	if len(items) != 1 {
		return "\x00"
	}

	return items[0]
}

func queryOrigin(
	values url.Values,
) (string, error) {
	raw := queryValue(values, "origin")
	if raw == "" {
		return "", nil
	}

	if raw == "\x00" {
		return "", errInvalidOriginFilter
	}

	parsed, err := origin.Parse(raw)
	if err != nil || parsed.String() == "" {
		return "", errInvalidOriginFilter
	}

	return parsed.String(), nil
}

func queryBool(
	values url.Values,
	name string,
) (*bool, error) {
	raw := queryValue(values, name)
	if raw == "" {
		return nil, nil
	}

	var value bool

	switch raw {
	case "true":
		value = true
	case "false":
		value = false
	default:
		return nil, errInvalidFilter
	}

	return &value, nil
}

func queryEnum(
	values url.Values,
	name string,
	allowed map[string]struct{},
) (string, error) {
	raw := queryValue(values, name)
	if raw == "" {
		return "", nil
	}

	if _, exists := allowed[raw]; !exists {
		return "", errInvalidFilter
	}

	return raw, nil
}

func encodeCursor(
	payload cursorPayload,
) string {
	encoded, _ := json.Marshal(payload)

	return base64.RawURLEncoding.EncodeToString(
		encoded,
	)
}

func decodeCursor(
	raw string,
	kind string,
) (cursorPayload, error) {
	if raw == "" {
		return cursorPayload{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(
		raw,
	)
	if err != nil {
		return cursorPayload{}, errInvalidCursor
	}

	decoder := json.NewDecoder(
		strings.NewReader(string(decoded)),
	)
	decoder.DisallowUnknownFields()

	var payload cursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return cursorPayload{}, errInvalidCursor
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(
		err,
		io.EOF,
	) {
		return cursorPayload{}, errInvalidCursor
	}

	if payload.Version != cursorVersion ||
		payload.Kind != kind {
		return cursorPayload{}, errInvalidCursor
	}

	return payload, nil
}

func decodeSourceCursor(
	raw string,
) (*sourceCursor, error) {
	if raw == "" {
		return nil, nil
	}

	payload, err := decodeCursor(raw, "sources")
	if err != nil ||
		payload.At != "" ||
		payload.ID != 0 ||
		!canonicalCursorOrigin(payload.Origin) {
		return nil, errInvalidCursor
	}

	return &sourceCursor{
		Origin: payload.Origin,
	}, nil
}

func decodeCrawlCursor(
	raw string,
) (*crawlCursor, error) {
	if raw == "" {
		return nil, nil
	}

	payload, err := decodeCursor(raw, "crawls")
	if err != nil ||
		payload.Origin != "" ||
		payload.ID <= 0 {
		return nil, errInvalidCursor
	}

	startedAt, err := time.Parse(
		time.RFC3339Nano,
		payload.At,
	)
	if err != nil || startedAt.IsZero() {
		return nil, errInvalidCursor
	}

	return &crawlCursor{
		StartedAt: startedAt.UTC(),
		ID:        payload.ID,
	}, nil
}

func decodeQueueCursor(
	raw string,
) (*queueCursor, error) {
	if raw == "" {
		return nil, nil
	}

	payload, err := decodeCursor(raw, "queue")
	if err != nil ||
		payload.ID != 0 ||
		!canonicalCursorOrigin(payload.Origin) {
		return nil, errInvalidCursor
	}

	availableAt, err := time.Parse(
		time.RFC3339Nano,
		payload.At,
	)
	if err != nil || availableAt.IsZero() {
		return nil, errInvalidCursor
	}

	return &queueCursor{
		AvailableAt: availableAt.UTC(),
		Origin:      payload.Origin,
	}, nil
}

func decodeQueueEventCursor(
	raw string,
) (*queueEventCursor, error) {
	if raw == "" {
		return nil, nil
	}

	payload, err := decodeCursor(
		raw,
		"queue_events",
	)
	if err != nil ||
		payload.Origin != "" ||
		payload.ID <= 0 {
		return nil, errInvalidCursor
	}

	occurredAt, err := time.Parse(
		time.RFC3339Nano,
		payload.At,
	)
	if err != nil || occurredAt.IsZero() {
		return nil, errInvalidCursor
	}

	return &queueEventCursor{
		OccurredAt: occurredAt.UTC(),
		ID:         payload.ID,
	}, nil
}

func decodeAuditCursor(raw string) (*auditCursor, error) {
	if raw == "" {
		return nil, nil
	}
	payload, err := decodeCursor(raw, "audit")
	if err != nil || payload.Origin != "" || payload.ID <= 0 {
		return nil, errInvalidCursor
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, payload.At)
	if err != nil || occurredAt.IsZero() {
		return nil, errInvalidCursor
	}
	return &auditCursor{OccurredAt: occurredAt.UTC(), ID: payload.ID}, nil
}

func canonicalCursorOrigin(
	raw string,
) bool {
	if raw == "" {
		return false
	}

	parsed, err := origin.Parse(raw)
	if err != nil {
		return false
	}

	return parsed.String() == raw
}

func sourceNextCursor(
	item CrawlSource,
) string {
	return encodeCursor(
		cursorPayload{
			Version: cursorVersion,
			Kind:    "sources",
			Origin:  item.Origin,
		},
	)
}

func crawlNextCursor(
	item CrawlRun,
) string {
	return encodeCursor(
		cursorPayload{
			Version: cursorVersion,
			Kind:    "crawls",
			At: item.StartedAt.UTC().Format(
				time.RFC3339Nano,
			),
			ID: item.ID,
		},
	)
}

func queueNextCursor(
	item QueueItem,
) string {
	return encodeCursor(
		cursorPayload{
			Version: cursorVersion,
			Kind:    "queue",
			Origin:  item.Origin,
			At: item.AvailableAt.UTC().Format(
				time.RFC3339Nano,
			),
		},
	)
}

func queueEventNextCursor(
	item QueueEvent,
) string {
	return encodeCursor(
		cursorPayload{
			Version: cursorVersion,
			Kind:    "queue_events",
			At: item.OccurredAt.UTC().Format(
				time.RFC3339Nano,
			),
			ID: item.ID,
		},
	)
}

func auditNextCursor(item AuditEvent) string {
	return encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    "audit",
		At:      item.OccurredAt.UTC().Format(time.RFC3339Nano),
		ID:      item.ID,
	})
}

func writeQueryError(
	writer http.ResponseWriter,
	err error,
) {
	code := "invalid_filter"

	switch {
	case errors.Is(err, errInvalidLimit):
		code = "invalid_limit"
	case errors.Is(err, errInvalidCursor):
		code = "invalid_cursor"
	case errors.Is(err, errInvalidOriginFilter):
		code = "invalid_origin"
	}

	writeError(
		writer,
		http.StatusBadRequest,
		code,
	)
}

func writePage(
	writer http.ResponseWriter,
	nextCursor string,
	items any,
) {
	if nextCursor != "" {
		writer.Header().Set(
			nextCursorHeader,
			nextCursor,
		)
	}

	writeJSON(
		writer,
		http.StatusOK,
		items,
	)
}

func pageLimit(
	limit int,
) (int, error) {
	if limit < 1 || limit > maxLimit {
		return 0, errInvalidLimit
	}

	return limit + 1, nil
}

func trimSourcePage(
	items []CrawlSource,
	limit int,
) sourcePage {
	if items == nil {
		items = []CrawlSource{}
	}

	if len(items) <= limit {
		return sourcePage{Items: items}
	}

	items = items[:limit]

	return sourcePage{
		Items: items,
		NextCursor: sourceNextCursor(
			items[len(items)-1],
		),
	}
}

func trimCrawlPage(
	items []CrawlRun,
	limit int,
) crawlPage {
	if items == nil {
		items = []CrawlRun{}
	}

	if len(items) <= limit {
		return crawlPage{Items: items}
	}

	items = items[:limit]

	return crawlPage{
		Items: items,
		NextCursor: crawlNextCursor(
			items[len(items)-1],
		),
	}
}

func trimQueuePage(
	items []QueueItem,
	limit int,
) queuePage {
	if items == nil {
		items = []QueueItem{}
	}

	if len(items) <= limit {
		return queuePage{Items: items}
	}

	items = items[:limit]

	return queuePage{
		Items: items,
		NextCursor: queueNextCursor(
			items[len(items)-1],
		),
	}
}

func trimQueueEventPage(
	items []QueueEvent,
	limit int,
) queueEventPage {
	if items == nil {
		items = []QueueEvent{}
	}

	if len(items) <= limit {
		return queueEventPage{Items: items}
	}

	items = items[:limit]

	return queueEventPage{
		Items: items,
		NextCursor: queueEventNextCursor(
			items[len(items)-1],
		),
	}
}

func trimAuditPage(items []AuditEvent, limit int) auditPage {
	if items == nil {
		items = []AuditEvent{}
	}
	if len(items) <= limit {
		return auditPage{Items: items}
	}
	items = items[:limit]
	return auditPage{
		Items:      items,
		NextCursor: auditNextCursor(items[len(items)-1]),
	}
}
