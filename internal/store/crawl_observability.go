package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

var (
	errInvalidCrawlRun     = errors.New("store: crawl run is invalid")
	errInvalidPageAttempt  = errors.New("store: crawl page attempt is invalid")
	errInvalidRetention    = errors.New("store: crawl retention is invalid")
	errInvalidServiceState = errors.New("store: crawl service state is invalid")
	errUnknownCrawlRun     = errors.New("store: crawl run is unknown")
)

// CrawlControl is the persistent operator pause state shared by processors.
type CrawlControl struct {
	DiscoveryPaused    bool
	VerificationPaused bool
	UpdatedAt          time.Time
}

// ServiceHeartbeat is a bounded status update from a long-running process.
type ServiceHeartbeat struct {
	Service       string
	InstanceID    string
	State         string
	CurrentOrigin origin.Origin
	Message       string
	StartedAt     time.Time
	UpdatedAt     time.Time
}

// BeginCrawl creates an incomplete run before any page request is attempted.
func (s *DiscoveryStore) BeginCrawl(
	ctx context.Context,
	source origin.Origin,
	config discovery.CrawlConfig,
) (discovery.CrawlRunID, error) {
	if err := s.validate(ctx); err != nil {
		return 0, err
	}
	if source.String() == "" || !validTelemetryConfig(config) {
		return 0, errInvalidCrawlRun
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO crawl_runs (
			source_origin, started_at, max_depth, max_pages, max_page_bytes,
			request_delay_milliseconds, redirect_limit,
			page_timeout_milliseconds, max_automatic_promotions
		)
		VALUES (
			$1, statement_timestamp(), $2, $3, $4, $5, $6, $7, $8
		)
		RETURNING id
	`, source.String(), config.MaxDepth, config.MaxPages, config.MaxPageBytes,
		config.RequestDelay.Milliseconds(), config.RedirectLimit,
		config.PageTimeout.Milliseconds(),
		automaticPromotionBudget(config, s.automatic)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: begin crawl: %w", err)
	}
	return discovery.CrawlRunID(id), nil
}

func automaticPromotionBudget(
	config discovery.CrawlConfig,
	automatic AutomaticCrawlConfig,
) int {
	if config.MaxAutomaticPromotionsPerRun > 0 {
		return config.MaxAutomaticPromotionsPerRun
	}
	return automatic.maxAutomaticPromotionsPerRun()
}

// RecordPageAttempt appends one ordered, sanitized page result.
func (s *DiscoveryStore) RecordPageAttempt(
	ctx context.Context,
	runID discovery.CrawlRunID,
	attempt discovery.PageAttempt,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if runID <= 0 || !validPageAttempt(attempt) {
		return errInvalidPageAttempt
	}

	var status any
	if attempt.StatusCode != nil {
		status = *attempt.StatusCode
	}
	result, err := s.pool.Exec(ctx, `
		INSERT INTO crawl_page_attempts (
			run_id, sequence, requested_url, final_url, depth, started_at,
			duration_milliseconds, status_code, response_bytes, content_type,
			redirect_count, robots_decision, internal_link_count,
			external_link_count, outcome, failure_category, urls_found,
			urls_enqueued
		)
		SELECT
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18
		WHERE EXISTS (
			SELECT 1 FROM crawl_runs WHERE id = $1 AND finished_at IS NULL
		)
	`, int64(runID), attempt.Sequence, attempt.RequestedURL, attempt.FinalURL,
		attempt.Depth, attempt.StartedAt.UTC(), attempt.Duration.Milliseconds(),
		status, attempt.ResponseBytes, attempt.ContentType,
		attempt.RedirectCount, string(attempt.RobotsDecision),
		attempt.InternalLinkCount, attempt.ExternalLinkCount,
		string(attempt.Outcome), storedFailureCategory(attempt.FailureCategory),
		attempt.URLsFound, attempt.URLsEnqueued)
	if err != nil {
		return fmt.Errorf("store: record crawl page: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errUnknownCrawlRun
	}
	return nil
}

// FinishCrawl seals a run with its final bounded summary.
func (s *DiscoveryStore) FinishCrawl(
	ctx context.Context,
	runID discovery.CrawlRunID,
	result discovery.CrawlResult,
	outcome discovery.CrawlRunOutcome,
	stopReason string,
) error {
	return s.FinishCrawlSummary(ctx, runID, result, outcome, stopReason, 0)
}

// FinishCrawlSummary seals a run and records its remaining bounded frontier.
func (s *DiscoveryStore) FinishCrawlSummary(
	ctx context.Context,
	runID discovery.CrawlRunID,
	result discovery.CrawlResult,
	outcome discovery.CrawlRunOutcome,
	stopReason string,
	frontierRemaining int,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if runID <= 0 || !validRunOutcome(outcome) || len(stopReason) > 128 ||
		result.PagesAttempted < 0 || result.PagesParsed < 0 ||
		result.CandidatesDiscovered < 0 || frontierRemaining < 0 {
		return errInvalidCrawlRun
	}

	databaseResult, err := s.pool.Exec(ctx, `
		UPDATE crawl_runs
		SET finished_at = statement_timestamp(), outcome = $2,
			stop_reason = $3, pages_attempted = $4, pages_parsed = $5,
			candidates_discovered = $6, budget_exhausted = $7,
			failure_category = CASE WHEN $8 = '' THEN 'none' ELSE $8 END,
			pages_blocked = (
				SELECT count(*) FROM crawl_page_attempts
				WHERE run_id = $1 AND outcome = 'robots_denied'
			),
			pages_failed = (
				SELECT count(*) FROM crawl_page_attempts
				WHERE run_id = $1
					AND outcome NOT IN ('complete', 'redirected', 'robots_denied')
			),
			urls_found = (
				SELECT COALESCE(sum(urls_found), 0) FROM crawl_page_attempts
				WHERE run_id = $1
			),
			urls_enqueued = (
				SELECT COALESCE(sum(urls_enqueued), 0) FROM crawl_page_attempts
				WHERE run_id = $1
			),
			frontier_remaining = $9,
			promotions_admitted = (
				SELECT count(DISTINCT batch.candidate_origin)
				FROM crawl_run_automatic_admission_batches AS batch
				WHERE batch.discovered_run_id = $1
					AND batch.outcome = 'promoted'
			),
			promotions_deferred = (
				SELECT GREATEST(
					count(*) - (
						SELECT count(DISTINCT batch.candidate_origin)
						FROM crawl_run_automatic_admission_batches AS batch
						WHERE batch.discovered_run_id = $1
							AND batch.outcome = 'promoted'
					),
					0
				)
				FROM crawl_run_discovery_candidates AS candidate
				WHERE candidate.run_id = $1
					AND candidate.kind = 'link'
					AND candidate.run_id = (
						SELECT min(original.run_id)
						FROM crawl_run_discovery_candidates AS original
						WHERE original.candidate_origin =
								candidate.candidate_origin
							AND original.kind = 'link'
					)
			)
		WHERE id = $1 AND finished_at IS NULL
	`, int64(runID), string(outcome), stopReason, result.PagesAttempted,
		result.PagesParsed, result.CandidatesDiscovered,
		result.BudgetExhausted, storedFailureCategory(result.FailureCategory),
		frontierRemaining)
	if err != nil {
		return fmt.Errorf("store: finish crawl: %w", err)
	}
	if databaseResult.RowsAffected() != 1 {
		return errUnknownCrawlRun
	}
	return nil
}

// PurgeCrawlTelemetry removes completed runs older than retention. Page rows
// are removed by the run foreign key. Running attempts are never purged.
func (s *DiscoveryStore) PurgeCrawlTelemetry(
	ctx context.Context,
	retention time.Duration,
) (int64, error) {
	if err := s.validate(ctx); err != nil {
		return 0, err
	}
	if retention <= 0 {
		return 0, errInvalidRetention
	}

	result, err := s.pool.Exec(ctx, `
		DELETE FROM crawl_runs
		WHERE finished_at IS NOT NULL
			AND finished_at < statement_timestamp() - make_interval(
				secs => $1::double precision
			)
	`, retention.Seconds())
	if err != nil {
		return 0, fmt.Errorf("store: purge crawl telemetry: %w", err)
	}
	return result.RowsAffected(), nil
}

// CrawlControl returns the persistent automatic-crawler control state.
func (s *DiscoveryStore) CrawlControl(
	ctx context.Context,
) (CrawlControl, error) {
	if err := s.validate(ctx); err != nil {
		return CrawlControl{}, err
	}
	var control CrawlControl
	err := s.pool.QueryRow(ctx, `
		SELECT discovery_paused, verification_paused, updated_at
		FROM crawl_control WHERE singleton
	`).Scan(
		&control.DiscoveryPaused,
		&control.VerificationPaused,
		&control.UpdatedAt,
	)
	if err != nil {
		return CrawlControl{}, fmt.Errorf("store: read crawl control: %w", err)
	}
	return control, nil
}

// DiscoveryPaused reports the durable discovery processor pause state.
func (s *DiscoveryStore) DiscoveryPaused(ctx context.Context) (bool, error) {
	control, err := s.CrawlControl(ctx)
	if err != nil {
		return false, err
	}
	return control.DiscoveryPaused, nil
}

// SetProcessorPaused atomically updates one shared processor pause state.
func (s *DiscoveryStore) SetProcessorPaused(
	ctx context.Context,
	processor string,
	paused bool,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	var column string
	switch processor {
	case "discovery":
		column = "discovery_paused"
	case "verification":
		column = "verification_paused"
	default:
		return errInvalidServiceState
	}
	_, err := s.pool.Exec(
		ctx,
		"UPDATE crawl_control SET "+column+
			" = $1, updated_at = statement_timestamp() WHERE singleton",
		paused,
	)
	if err != nil {
		return fmt.Errorf("store: set crawl control: %w", err)
	}
	return nil
}

// UpsertServiceHeartbeat records current process state without raw errors.
func (s *DiscoveryStore) UpsertServiceHeartbeat(
	ctx context.Context,
	heartbeat ServiceHeartbeat,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if !validHeartbeat(heartbeat) {
		return errInvalidServiceState
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO crawl_service_heartbeats (
			service, instance_id, state, current_origin, message,
			started_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (service, instance_id) DO UPDATE
		SET state = CASE
				WHEN EXCLUDED.updated_at >= crawl_service_heartbeats.updated_at
				THEN EXCLUDED.state
				ELSE crawl_service_heartbeats.state
			END,
			current_origin = CASE
				WHEN EXCLUDED.updated_at >= crawl_service_heartbeats.updated_at
				THEN EXCLUDED.current_origin
				ELSE crawl_service_heartbeats.current_origin
			END,
			message = CASE
				WHEN EXCLUDED.updated_at >= crawl_service_heartbeats.updated_at
				THEN EXCLUDED.message
				ELSE crawl_service_heartbeats.message
			END,
			started_at = LEAST(
				crawl_service_heartbeats.started_at,
				EXCLUDED.started_at
			),
			updated_at = GREATEST(
				crawl_service_heartbeats.updated_at,
				EXCLUDED.updated_at
			)
	`, heartbeat.Service, heartbeat.InstanceID, heartbeat.State,
		heartbeat.CurrentOrigin.String(), heartbeat.Message,
		heartbeat.StartedAt.UTC(), heartbeat.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("store: write service heartbeat: %w", err)
	}
	return nil
}

func validTelemetryConfig(config discovery.CrawlConfig) bool {
	return config.MaxDepth >= 0 && config.MaxPages > 0 &&
		config.MaxPageBytes > 0 && config.RequestDelay >= 0 &&
		config.RedirectLimit >= 0 && config.PageTimeout > 0 &&
		config.MaxAutomaticPromotionsPerRun >= 0
}

func validPageAttempt(attempt discovery.PageAttempt) bool {
	return attempt.Sequence > 0 && attempt.Depth >= 0 &&
		!attempt.StartedAt.IsZero() && attempt.Duration >= 0 &&
		attempt.ResponseBytes >= 0 && attempt.RedirectCount >= 0 &&
		attempt.InternalLinkCount >= 0 && attempt.ExternalLinkCount >= 0 &&
		attempt.URLsFound >= 0 && attempt.URLsEnqueued >= 0 &&
		len(attempt.ContentType) <= 256 && safeStoredPageURL(attempt.RequestedURL) &&
		safeStoredPageURL(attempt.FinalURL) && validRobotsDecision(attempt.RobotsDecision) &&
		validPageOutcome(attempt.Outcome) && validStatusCode(attempt.StatusCode) &&
		attempt.FailureCategory.Valid()
}

func safeStoredPageURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.User == nil && parsed.Host != "" &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.RawQuery == "" && parsed.Fragment == "" && !parsed.ForceQuery
}

func validRobotsDecision(decision discovery.RobotsDecision) bool {
	return decision == discovery.RobotsAllowed ||
		decision == discovery.RobotsDenied || decision == discovery.RobotsUnknown
}

func validPageOutcome(outcome discovery.PageOutcome) bool {
	switch outcome {
	case discovery.PageComplete, discovery.PageRedirected,
		discovery.PageRobotsDenied, discovery.PageUnsupportedContent,
		discovery.PageTooLarge, discovery.PageHTTPError,
		discovery.PageTimeout, discovery.PageNetworkError,
		discovery.PageInvalidResponse:
		return true
	default:
		return false
	}
}

func validRunOutcome(outcome discovery.CrawlRunOutcome) bool {
	return outcome == discovery.CrawlRunComplete ||
		outcome == discovery.CrawlRunBudgetExhausted ||
		outcome == discovery.CrawlRunCanceled ||
		outcome == discovery.CrawlRunFailed
}

func validStatusCode(status *int) bool {
	return status == nil || (*status >= 100 && *status <= 599)
}

func storedFailureCategory(category retry.Category) string {
	if category == retry.CategoryNone {
		return "none"
	}
	return string(category)
}

func validHeartbeat(heartbeat ServiceHeartbeat) bool {
	validService := heartbeat.Service == "discovery" || heartbeat.Service == "worker"
	validState := heartbeat.State == "starting" || heartbeat.State == "idle" ||
		heartbeat.State == "running" || heartbeat.State == "paused" ||
		heartbeat.State == "stopping" || heartbeat.State == "failed"
	return validService && validState && strings.TrimSpace(heartbeat.InstanceID) != "" &&
		len(heartbeat.InstanceID) <= 128 && len(heartbeat.Message) <= 256 &&
		!heartbeat.StartedAt.IsZero() && !heartbeat.UpdatedAt.IsZero() &&
		!heartbeat.UpdatedAt.Before(heartbeat.StartedAt)
}
