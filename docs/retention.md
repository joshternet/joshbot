# Data retention

JoshBot separates durable knowledge from disposable operational history.

The crawler may continue discovering origins indefinitely. Retention therefore
does not mean forgetting an origin simply because it has not been seen
recently. Durable participation state, discovery provenance, operator policy,
and current scheduling state remain independent from the event history that
can be discarded.

Operational-history cleanup runs when the discovery service starts. A 30-day
retention threshold means that eligible records older than 30 days are removed
during that maintenance pass. It does not mean that a continuously running
process deletes an individual row at the instant it reaches 30 days.

## Verification observations

Ordinary `verification_observations` older than the 30-day operational-history
threshold are eligible for cleanup.

Two observations for an origin may survive longer than 30 days when they are
still required to preserve current semantics:

- the latest observation of any outcome;
- the latest authoritative declaration observation.

An authoritative declaration observation is one of `valid`, `absent`,
`invalid`, `unsupported_version`, or `cross_origin_redirect`.

This preserves the distinction between authoritative declaration state and
temporary conditions such as network unavailability or robots denial. Cleanup
therefore cannot turn a temporary retrieval failure into a participation
change.

The `origins` table separately preserves `first_observed_at`,
`first_participated_at`, the initial valid declaration, and the latest
declaration check. Retention never deletes those facts.

## Reprobe scheduling state

Reprobe scheduling does not depend on disposable observation history.

`verification_reprobe_state` stores the cumulative count of outcomes that
contribute to the existing reprobe backoff calculation. It contains at most
one row per origin and is retained with that origin.

Migration `0022_history_retention.sql` backfills this count from existing
observations before any history is deleted. New verification results update
the compact counter atomically with the observation.

The outcomes counted for reprobe backoff are:

- `absent`;
- `invalid`;
- `unsupported_version`;
- `robots_denied`;
- `cross_origin_redirect`.

Temporary `unavailable` results do not increment this counter.

## Verification queue

`verification_queue` is current scheduling state and is not age-purged.

`verification_queue_events` older than the 30-day operational-history
threshold are eligible for cleanup during discovery startup maintenance.

Removing queue-event history does not remove queued work, lease state, retry
state, or reprobe state.

## Crawl telemetry

Completed crawl telemetry continues to use its existing configurable
retention policy, which defaults to 30 days.

The policy is controlled by:

```text
JOSHBOT_CRAWL_TELEMETRY_RETENTION
```

Eligible completed telemetry is removed during discovery startup maintenance.

Deleting a completed `crawl_runs` row cascades to its run-scoped
`crawl_page_attempts`, `crawl_run_discovery_candidates`, and
`crawl_run_automatic_admission_batches` rows.

Running crawl attempts are never removed by retention cleanup.

## Discovery provenance

Discovery knowledge is compacted rather than age-expired.

`discovery_candidates` keeps one row per candidate origin with its first and
last discovery times.

`discovery_edges` keeps one row per source origin, candidate origin, and
discovery kind with its first and last discovery times.

`discovery_source_state` is current durable crawler knowledge and is retained.

`discovery_source_schedule` is active crawl intent. A missing row means that
origin is not scheduled. Removing it does not delete candidates, edges, or
`discovery_source_state`.

A source becoming cold, blocked, unscheduled, or otherwise absent from recent
crawl activity does not delete its discovery provenance.

## Participation state

Compact participation metadata on `origins` is durable and is not subject to
history retention.

This includes:

- first observation time;
- first participation time;
- initial valid declaration;
- latest completed declaration-check time;
- latest completed declaration-check outcome.

The public registry projects only the participation facts needed for registry
history: first participation time, initial declaration, latest declaration-check
time, latest declaration-check outcome, and the current declaration when the
origin is currently participating.

First observation time remains private operational metadata and is not published
in the registry. The registry also does not derive or publish separate status
fields such as `participation_status`, `is_active`, or `ever_participated`.

Retention cleanup does not reinterpret these durable facts. It ensures that
disposable observation history cannot erase the state needed to preserve the
same registry and participation semantics after cleanup.

## Service heartbeats

`crawl_service_heartbeats` is current service-slot status, not audit history.

Heartbeat slots whose `updated_at` value is older than the 30-day
operational-history threshold are eligible for removal during discovery startup
maintenance.

Active and recently stopped logical service slots remain available for
operational reporting.

Stable configured service instance IDs keep this table naturally compact.
Stale-slot cleanup also bounds rows left behind by changed or ephemeral
instance identifiers.

## Operator audit events

`operator_audit_events` is reviewed independently from crawler telemetry.

Operator audit events remain append-only and are retained indefinitely. The
operational-history cleanup has no code path that deletes them.

This preserves the existing audit boundary rather than weakening it merely to
make audit data follow crawler-history retention.

## Other current-state tables

`crawl_control`, `crawl_domain_avoid_rules`, and other operator-controlled
configuration tables are current state rather than event history and are not
age-purged.

`origins` is durable knowledge and is not deleted because an origin becomes
inactive.

Current verification queue rows, discovery source state, explicit block state,
automatic-source state, and curated seed state are likewise not removed merely
because they are old.

## Migration metadata

`schema_migrations` is durable database metadata and is retained indefinitely.

It contains one row for each applied migration. It is not crawler history and
is not subject to age-based cleanup.

## Cleanup execution

The discovery service performs operational-history cleanup when it starts,
before it begins a new service heartbeat or discovery work.

The fixed operational-history threshold is 30 days for:

- ordinary verification observations, subject to semantic protection;
- verification queue-event history;
- stale service heartbeat slots.

Crawl telemetry continues to use its existing
`JOSHBOT_CRAWL_TELEMETRY_RETENTION` setting and 30-day default.

A separate configuration setting for operational history is intentionally not
introduced. The current requirement establishes one retention policy, and a
new tuning surface is not justified until there is an operational need for
one.

## Retention and protocol semantics

Retention is an implementation concern. It does not change the semantics of
RFC-JOSH-0002 or create a new participation state.

A valid declaration remains authoritative participation. An authoritative
absence or invalid declaration can end that state according to the existing
verification rules. Temporary retrieval failures still do not establish
withdrawal.

History cleanup must therefore preserve enough state to produce the same
effective participation result before and after cleanup.
