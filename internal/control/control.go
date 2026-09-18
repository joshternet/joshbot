package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

const (
	DefaultActor        = "operator-api"
	maxRequestBodyBytes = 4096
	maxActorBytes       = 128
	maxActionBytes      = 128
	maxCallerBytes      = 256
	maxReasonBytes      = 512
	maxTargetBytes      = 2048
	minTokenBytes       = 32
)

const (
	ResultSuccess  = "success"
	ResultRejected = "rejected"
)

var (
	errCommanderUnavailable = errors.New("control: commander is unavailable")
	errTokenUnavailable     = errors.New("control: authentication token is invalid")
	domainPattern           = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?:\.\*)?$`)
)

// Audit describes one bounded authenticated mutation attempt.
type Audit struct {
	OccurredAt time.Time
	Action     string
	Target     string
	Caller     string
	Actor      string
	Result     string
	Reason     string
}

// Commander applies control mutations and their success audits atomically.
// RecordRejected records authenticated attempts that did not mutate state.
type Commander interface {
	SetProcessorPaused(context.Context, string, bool, Audit) error
	AddDomainAvoid(context.Context, string, Audit) error
	RemoveDomainAvoid(context.Context, string, Audit) error
	SetOriginBlocked(context.Context, string, bool, Audit) error
	RecordRejected(context.Context, Audit) error
}

type handler struct {
	commander                 Commander
	expectedAuthorizationHash [sha256.Size]byte
	now                       func() time.Time
}

type metadata struct {
	Actor  string `json:"actor,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type originRequest struct {
	Origin string `json:"origin"`
	Actor  string `json:"actor,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type domainRequest struct {
	Pattern string `json:"pattern"`
	Actor   string `json:"actor,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// NewHandler constructs the private operator control HTTP handler.
func NewHandler(commander Commander, token string) (http.Handler, error) {
	if commander == nil {
		return nil, errCommanderUnavailable
	}
	if !validToken(token) {
		return nil, errTokenUnavailable
	}
	runtime := &handler{
		commander: commander,
		expectedAuthorizationHash: authorizationDigest(
			"Bearer " + token,
		),
		now: time.Now,
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/control/processors/{processor}/{operation}", runtime.processor)
	api.HandleFunc("POST /api/v1/control/domain-avoid", runtime.addDomainAvoid)
	api.HandleFunc("DELETE /api/v1/control/domain-avoid/{pattern}", runtime.removeDomainAvoid)
	api.HandleFunc("POST /api/v1/control/origins/{operation}", runtime.origin)
	api.HandleFunc("/api/v1/control/", runtime.invalidControlRequest)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", runtime.health)
	root.Handle("/api/", runtime.authorize(api))
	return runtime.withHeaders(root), nil
}

func validToken(token string) bool {
	if len(token) < minTokenBytes || len(token) > maxRequestBodyBytes ||
		strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
		return false
	}
	unique := make(map[rune]struct{})
	for _, character := range token {
		if character < '!' || character > '~' {
			return false
		}
		unique[character] = struct{}{}
	}
	return len(unique) >= 8
}

func (h *handler) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func (h *handler) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !authorizationMatches(
			h.expectedAuthorizationHash,
			request.Header.Get("Authorization"),
		) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="joshbot-operator"`)
			writeError(writer, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func authorizationDigest(value string) [sha256.Size]byte {
	return sha256.Sum256([]byte(value))
}

func authorizationMatches(
	expected [sha256.Size]byte,
	provided string,
) bool {
	providedHash := authorizationDigest(provided)
	return subtle.ConstantTimeCompare(
		providedHash[:],
		expected[:],
	) == 1
}

func (h *handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) invalidControlRequest(
	writer http.ResponseWriter,
	request *http.Request,
) {
	audit := h.newAudit(
		request,
		"request.reject",
		boundedTarget(request.URL.Path),
	)
	status := http.StatusNotFound
	code := "not_found"
	if knownControlPath(request.URL.Path) {
		status = http.StatusMethodNotAllowed
		code = "method_not_allowed"
	}
	h.reject(writer, request, audit, status, code)
}

func knownControlPath(path string) bool {
	if path == "/api/v1/control/domain-avoid" {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/control/domain-avoid/") {
		return strings.TrimPrefix(path, "/api/v1/control/domain-avoid/") != ""
	}
	if strings.HasPrefix(path, "/api/v1/control/processors/") {
		parts := strings.Split(strings.TrimPrefix(
			path,
			"/api/v1/control/processors/",
		), "/")
		return len(parts) == 2 &&
			(parts[0] == "discovery" || parts[0] == "verification") &&
			(parts[1] == "pause" || parts[1] == "resume")
	}
	return path == "/api/v1/control/origins/block" ||
		path == "/api/v1/control/origins/allow"
}

func (h *handler) processor(writer http.ResponseWriter, request *http.Request) {
	processor := request.PathValue("processor")
	operation := request.PathValue("operation")
	action := "processor." + operation
	audit := h.newAudit(request, action, boundedTarget(processor))
	if (processor != "discovery" && processor != "verification") ||
		(operation != "pause" && operation != "resume") {
		h.reject(writer, request, audit, http.StatusNotFound, "not_found")
		return
	}
	var body metadata
	if status, code := decodeOptionalJSON(writer, request, &body); status != 0 {
		h.reject(writer, request, audit, status, code)
		return
	}
	if !applyMetadata(&audit, body.Actor, body.Reason) {
		h.reject(writer, request, audit, http.StatusBadRequest, "invalid_request")
		return
	}
	h.mutate(writer, request, audit, func(success Audit) error {
		return h.commander.SetProcessorPaused(request.Context(), processor, operation == "pause", success)
	})
}

func (h *handler) addDomainAvoid(writer http.ResponseWriter, request *http.Request) {
	audit := h.newAudit(request, "domain-avoid.add", "")
	var body domainRequest
	if status, code := decodeJSON(writer, request, &body); status != 0 {
		h.reject(writer, request, audit, status, code)
		return
	}
	pattern := normalizeDomainPattern(body.Pattern)
	audit.Target = boundedTarget(pattern)
	metadataValid := applyMetadata(&audit, body.Actor, body.Reason)
	if !validDomainPattern(pattern) || !metadataValid {
		h.reject(writer, request, audit, http.StatusBadRequest, "invalid_request")
		return
	}
	h.mutate(writer, request, audit, func(success Audit) error {
		return h.commander.AddDomainAvoid(request.Context(), pattern, success)
	})
}

func (h *handler) removeDomainAvoid(writer http.ResponseWriter, request *http.Request) {
	pattern := normalizeDomainPattern(request.PathValue("pattern"))
	audit := h.newAudit(request, "domain-avoid.remove", boundedTarget(pattern))
	var body metadata
	if status, code := decodeOptionalJSON(writer, request, &body); status != 0 {
		h.reject(writer, request, audit, status, code)
		return
	}
	metadataValid := applyMetadata(&audit, body.Actor, body.Reason)
	if !validDomainPattern(pattern) || !metadataValid {
		h.reject(writer, request, audit, http.StatusBadRequest, "invalid_request")
		return
	}
	h.mutate(writer, request, audit, func(success Audit) error {
		return h.commander.RemoveDomainAvoid(request.Context(), pattern, success)
	})
}

func (h *handler) origin(writer http.ResponseWriter, request *http.Request) {
	operation := request.PathValue("operation")
	action := "origin." + operation
	audit := h.newAudit(request, action, "")
	if operation != "block" && operation != "allow" {
		h.reject(writer, request, audit, http.StatusNotFound, "not_found")
		return
	}
	var body originRequest
	if status, code := decodeJSON(writer, request, &body); status != 0 {
		h.reject(writer, request, audit, status, code)
		return
	}
	source, err := origin.Parse(body.Origin)
	if err == nil {
		audit.Target = boundedTarget(source.String())
	}
	metadataValid := applyMetadata(&audit, body.Actor, body.Reason)
	if err != nil || !metadataValid {
		h.reject(writer, request, audit, http.StatusBadRequest, "invalid_request")
		return
	}
	h.mutate(writer, request, audit, func(success Audit) error {
		return h.commander.SetOriginBlocked(
			request.Context(),
			source.String(),
			operation == "block",
			success,
		)
	})
}

func (h *handler) mutate(
	writer http.ResponseWriter,
	request *http.Request,
	audit Audit,
	mutation func(Audit) error,
) {
	audit.Result = ResultSuccess
	if err := mutation(audit); err != nil {
		audit.Result = ResultRejected
		_ = h.commander.RecordRejected(request.Context(), audit)
		writeError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) reject(
	writer http.ResponseWriter,
	request *http.Request,
	audit Audit,
	status int,
	code string,
) {
	audit.Result = ResultRejected
	if err := h.commander.RecordRejected(request.Context(), audit); err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	writeError(writer, status, code)
}

func (h *handler) newAudit(request *http.Request, action, target string) Audit {
	caller := request.RemoteAddr
	if host, _, err := net.SplitHostPort(caller); err == nil {
		caller = host
	}
	if caller == "" {
		caller = "unknown"
	}
	return Audit{
		OccurredAt: h.now().UTC(),
		Action:     bounded(action, maxActionBytes),
		Target:     boundedTarget(target),
		Caller:     bounded(caller, maxCallerBytes),
		Actor:      DefaultActor,
	}
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) (int, string) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, "unsupported_media_type"
	}
	if request.ContentLength > maxRequestBodyBytes {
		return http.StatusRequestEntityTooLarge, "request_too_large"
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes)
	defer request.Body.Close()
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, "request_too_large"
		}
		return http.StatusBadRequest, "invalid_request"
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, "request_too_large"
		}
		return http.StatusBadRequest, "invalid_request"
	}
	return 0, ""
}

func decodeOptionalJSON(
	writer http.ResponseWriter,
	request *http.Request,
	destination any,
) (int, string) {
	if request.ContentLength == 0 {
		return 0, ""
	}
	return decodeJSON(writer, request, destination)
}

func applyMetadata(audit *Audit, actor, reason string) bool {
	if actor != "" {
		if !validBoundedText(actor, maxActorBytes) || strings.TrimSpace(actor) != actor {
			return false
		}
		audit.Actor = actor
	}
	if reason != "" {
		if !validBoundedText(reason, maxReasonBytes) || strings.TrimSpace(reason) != reason {
			return false
		}
		audit.Reason = reason
	}
	return true
}

func validBoundedText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit &&
		strings.IndexFunc(value, func(character rune) bool {
			return character < ' ' || character == '\x7f'
		}) == -1
}

func normalizeDomainPattern(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func validDomainPattern(value string) bool {
	if len(value) < 1 || len(value) > 253 || !domainPattern.MatchString(value) {
		return false
	}
	family := strings.TrimSuffix(value, ".*")
	for _, label := range strings.Split(family, ".") {
		if len(label) < 1 || len(label) > 63 ||
			strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func boundedTarget(value string) string {
	return bounded(value, maxTargetBytes)
}

func bounded(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
