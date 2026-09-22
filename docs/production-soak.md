# Production soak

This guide is for operators running JoshBot `v1.1.0` against the real web after
deploy. The goal is to observe production behavior with the reporting surface
JoshBot already has, then open engineering work only when evidence requires it.

Adding a curated seed does **not** imply Joshternet membership, endorsement,
trust, or verified identity. Seeds are private operational discovery starting
points.

## Purpose

Answer one question:

> What does JoshBot actually do when we leave it running against the real web?

Run long enough to observe several normal discovery and verification cycles.
Do not invent a new analytics system. Prefer the private reporting API,
`/metrics`, operator control, and PostgreSQL operational tables already
deployed with `v1.1.0`.

## Soak checklist

Use the private reporting service (default `127.0.0.1:8788`, bearer token from
`JOSHBOT_REPORT_TOKEN_FILE`) and Compose health checks:

1. Confirm `worker` and `discovery` heartbeats are fresh via
   `GET /api/v1/services` or `joshbot_service_heartbeat_age_seconds`.
2. Confirm processors are not unexpectedly paused via `GET /api/v1/status`.
3. Record crawl-source counts by classification
   (`seeded`, `automatic`, `verified`, `blocked`, `crawl_eligible`).
4. Watch crawl runs complete via `GET /api/v1/crawls` and per-run detail.
5. Watch pages attempted versus pages parsed on recent runs.
6. Watch external origins found and candidates retained.
7. Watch automatic promotions versus deferred candidates and pending probes.
8. Watch verification queue depth for `probe` and `recurring` modes.
9. Sample retry streaks and failure categories from queue items and source
   detail where available.
10. Sample robots decisions from crawl page attempts and source detail
    `latest_robots_observation`.
11. Note blocked or avoided origins and whether they match operator intent.
12. After export/publish cycles, note public registry growth from the published
    tree (not from private crawl-source tables).
13. Log notable observations in the soak log below.
14. Open a GitHub issue only when evidence shows a concrete defect, operational
    deficiency, or clearly justified enhancement.

## Discovery funnel

Approximate funnel and existing data sources:

```text
crawl sources
      |
      v
crawl runs
      |
      v
pages parsed
      |
      v
external origins found
      |
      v
candidates retained
      |
      v
automatic sources admitted
      |
      v
verification probes
      |
      v
declarations checked
      |
      v
valid Joshternet participants
```

| Stage | Existing source |
| --- | --- |
| Crawl sources | `discovery_source_state`; `GET /api/v1/sources`; metric `joshbot_crawl_sources` |
| Crawl runs | `crawl_runs`; `GET /api/v1/crawls`; metric `joshbot_retained_crawl_runs` |
| Pages parsed | `crawl_runs.pages_parsed`; `crawl_page_attempts`; metrics `joshbot_retained_pages_attempted` / `joshbot_retained_pages_parsed` |
| External origins found | page/run counters; `discovery_candidates` + `discovery_edges`; metrics `joshbot_retained_origins_found`, `joshbot_retained_candidates`, `joshbot_retained_discovery_edges` |
| Candidates retained | `discovery_candidates`; `crawl_run_discovery_candidates`; `joshbot_retained_candidates` |
| Automatic sources admitted | `crawl_run_automatic_admission_batches`; `discovery_source_state.automatically_discovered`; metrics `joshbot_retained_origins_promoted` / `joshbot_retained_origins_deferred` |
| Verification probes | `verification_queue` where `mode='probe'`; `GET /api/v1/queue`; metrics `joshbot_verification_queue{mode="probe"}`, `joshbot_pending_probes` |
| Declarations checked | `verification_observations` in PostgreSQL (no dedicated reporting list endpoint) |
| Valid participants | latest authoritative `outcome='valid'` via store export / public registry files; reporting exposes verified **count** through sources/metrics, not a participant list API |

## Observation availability

Classify each soak question before asking for new telemetry.

### AVAILABLE NOW

| Observation | How |
| --- | --- |
| Service heartbeat state | `GET /api/v1/services`; `joshbot_service_heartbeat_age_seconds` |
| Crawl sources due / classified | `GET /api/v1/sources`; `GET /api/v1/status`; `joshbot_crawl_sources` |
| Crawl runs | `GET /api/v1/crawls`, `GET /api/v1/crawls/{id}` |
| Pages attempted / parsed | crawl run fields and page attempts; retained page metrics |
| External origins discovered | run/page counters; retained origin/candidate/edge metrics |
| Candidate retention | `discovery_candidates`; retained candidate metrics |
| Automatic admissions and deferred candidates | admission batch tables; promoted/deferred metrics; source classification |
| Pending verification probes | status backpressure fields; `joshbot_pending_probes`; probe queue metrics |
| Queue depth | `GET /api/v1/queue`; verification queue metrics |
| Robots denials (aggregate) | `joshbot_retained_robots_denials`; page attempt robots decisions |
| Latest robots observation for one origin | `GET /api/v1/sources/detail?origin=` |
| Blocked / avoided origins | source classifications; domain-avoid rules via operator control; audit log |
| Processor pause state | `GET /api/v1/status`; control API |
| Public registry growth | exported/published registry files |

### DERIVABLE NOW

| Observation | How to calculate |
| --- | --- |
| Parse success rate | `pages_parsed / pages_attempted` from crawl runs or retained metrics |
| Promotion versus deferral rate | `origins_promoted / (origins_promoted + origins_deferred)` from retained metrics or admission batches |
| Probe pressure | `pending_probes / pending_probe_limit` from status or metrics |
| Funnel drop from found origins to retained candidates | compare run `candidates_discovered` / retained candidates over a window |
| Funnel drop from admissions to probes | compare promotions admitted to probe queue growth over the same window |
| Verified participant growth | compare successive public `registry.json` exports or verified source counts over time |
| Retry-heavy origins | inspect queue items and source detail retry columns for high streaks |
| Lease recovery symptoms | compare heartbeat restarts, incomplete crawl runs, and later reclaimed source attempts in crawl/source history |

### NOT CURRENTLY AVAILABLE

Do not implement these during soak unless production evidence proves they are
necessary. Keep them as candidate follow-ups only:

- reporting list API for `verification_observations`
- reporting participant list of verified origins (beyond count / public export)
- first-class rediscovery rate (“same origin found again”) dashboard
- Crawl-delay observation time series distinct from per-request pacing
- dedicated retry-category dashboard endpoint
- automatic Cloudflare BotBase approval polling
- ML / AI candidate ranking or page classification
- generalized seed-scoring software

## Curated seed research

Seeds are managed only through the operator CLI:

```text
joshbot seed add <origin>
joshbot seed remove <origin>
joshbot seed list
```

There is no checked-in production seed corpus. Prefer a **small, diverse** set
of directories and indexes that lead toward personal sites and independent web
neighborhoods. Favor quality and neighborhood diversity over raw count.

### Evaluation rubric

For each proposed seed, record:

- URL / origin
- type of source
- why it is useful
- whether it appears actively maintained
- approximate breadth
- whether links are mostly direct public links
- whether it is likely to lead to personal sites
- whether robots policy permits JoshBot (check before adding)
- obvious duplication with another seed
- any reason to avoid crawling it

Avoid search-engine result pages, scraped spam indexes, link farms, and any
source that would undermine crawler behavior expectations.

### Initial shortlist

These are research candidates for operator review. None are endorsed
Joshternet participants. Robots permission and live usefulness must be
re-checked before `seed add`.

| Origin | Type | Why useful | Notes |
| --- | --- | --- | --- |
| `https://theindex.fyi` | Meta-index of indie/small-web indexes | High diversity pointer into multiple neighborhoods | Prefer as a small starting directory; may overlap other seeds |
| `https://personalsit.es` | Owner-submitted personal-site directory | Dense personal-site links | Good personal-web neighborhood; check robots and duplication |
| `https://xn--sr8hvo.ws` | IndieWeb webring | Community personal sites with IndieWeb building blocks | Use the public directory pages; webring navigation may be sparse |
| `https://indieweb.org` | IndieWeb wiki / community hub | Links into IndieWeb people and directories | Broad; may include non-personal pages—budget crawl limits matter |
| `https://nownownow.com` | `/now` page directory | Personal sites that publish `/now` | Distinct neighborhood from blogrolls |
| `https://blogscroll.com` | Open personal-site directory | Breadth of independent blogs | Confirm maintenance and robots before adding |
| `https://blogroll.org` | Human-curated blogroll-scale list ("Ye Olde Blogroll") | Large personal/independent blog neighborhood | Breadth is high; start only if robots allow and duplication is acceptable |
| Hacker News “personal websites” collection pages | Curated discussion-derived lists | Developer personal sites | Prefer a stable canonical URL the operator trusts; avoid ephemeral search pages |

Add at most a few approved origins at a time. Watch the funnel after each
batch before expanding further.

## Soak log

Keep a simple operator log (local notes are fine). For each notable
observation:

```text
date/time:
observation:
evidence:
impact:
possible explanation:
needs engineering change?: yes / no / unknown
```

This is not a ticket system. GitHub issues should be opened only when evidence
reveals a concrete defect, operational deficiency, or clearly justified
enhancement.

### 2026-09-21 — initial live snapshot on barf.lan

```text
date/time: 2026-09-21 ~22:03 UTC (dashboard ~3:02 PM local)
observation: Production JoshBot on barf.lan is actively discovering and verifying; funnel is very wide at the top and narrow at Joshternet membership.
evidence:
  - heartbeats: discovery+worker stable slots, ages ~1s; both often state=running
  - processors: discovery_paused=false, verification_paused=false
  - sources: seeded=6, automatic=82187, blocked≈15354, total≈82191
  - candidates=88379; discovery_edges≈231172
  - queue: probe≈965 (1 active lease, ~703 due), recurring=2
  - dashboard: Queued=1000, Active leases=1, Curated seeds=6, Joshternet=2
  - crawl run totals: pages_attempted=56729, pages_parsed=40365 (~71%),
    promotions_admitted=7265, promotions_deferred=13365,
    pages_blocked=3408, pages_failed=780
  - robots_decision: allowed=45883, denied=7535, unknown=3339
  - latest verification outcomes: valid=2, absent=43326, invalid=4019 (of 82976 latest-by-origin)
  - seeds: blogroll.org, joshing.you, nownownow.com, octothorp.es, personalsit.es, slashfriends.org
  - top retry streaks dominated by robots_temporary (streak 7–8)
  - recent crawls heavily include *.centerblog.net hosts
impact: Automatic expansion and verification are working; most candidates are absent declarations. Queue is near the pending-probe ceiling. Centerblog neighborhood may be amplifying low-value discovery.
possible explanation: Expected early-soak shape for a sparse identity protocol on the open web, plus blog-platform link density. Backpressure appears engaged (queued≈pending probe limit).
needs engineering change?: unknown — continue soak; do not open issues yet unless centerblog amplification or robots_temporary handling proves harmful beyond normal noise.
```

## Evidence-gated follow-up issues

When soak evidence justifies an issue, include:

```text
Observed evidence:
Current behavior:
Desired behavior:
Why this belongs in JoshBot:
Smallest reasonable change:
Acceptance criteria:
```

Examples that can justify follow-up after evidence:

- a real robots implementation JoshBot mishandles
- a redirect pattern that causes incorrect behavior
- queue starvation or excessive backlog
- repeated lease failures
- automatic discovery producing mostly junk
- useful candidates being systematically excluded
- telemetry insufficient to diagnose a recurring real failure
- unexpected crawl amplification
- misleading operational reporting
- excessive repeat discovery
- a security boundary failing under actual conditions
- Cloudflare verification exposing a protocol mismatch

Do not open issues merely because something might be useful someday.

## Cloudflare while waiting

Current accurate state:

```text
Cloudflare BotBase: submitted, awaiting review
```

Do not change Web Bot Auth merely because review is pending.

While awaiting review, operators may run:

```sh
joshbot conformance web-bot-auth --expect unregistered
```

A passing pre-registration check with the expected HTTP 401 result is **not**
Cloudflare approval.

When Cloudflare approves JoshBot:

1. Run `joshbot conformance web-bot-auth --expect verified` and require success.
2. Find JoshBot's public Cloudflare Radar Bot Directory entry.
3. Update the README crawler identity section with that public Radar entry.
4. Accurately state that JoshBot is Cloudflare Verified via Web Bot Auth.
5. Do not alter the working signing implementation unless verification exposes
   a real problem.

## Related documentation

- [Deployment guide](../deploy/README.md)
- [Crawler behavior](crawler.md)
- [Architecture](architecture.md)
- [Changelog](../CHANGELOG.md)
