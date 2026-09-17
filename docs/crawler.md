# JoshBot crawler

JoshBot discovers possible Joshternet origins while enforcing network,
robots, resource, and storage boundaries.

## Identity

JoshBot sends this exact HTTP User-Agent:

```text
Joshternet-Joshbot (+https://joshternet.org/joshbot)
```

The crawler information URL is:

```text
https://joshternet.org/joshbot
```

The RFC 9309 product token used for robots matching is:

```text
Joshternet-Joshbot
```

## Why JoshBot crawls

JoshBot follows eligible public links to discover web origins that may
participate in the Joshternet.

A link creates a candidate. It does not make the linked origin trusted,
verified, or public.

Each candidate must independently publish a valid Joshternet declaration at:

```text
/.well-known/josh
```

before it can appear as a participant in the public registry.

## Crawling and participation are independent

JoshBot treats two decisions separately:

- `robots.txt` determines whether JoshBot may retrieve a URI;
- `/.well-known/josh` declares whether an origin participates in the
  Joshternet.

A permissive robots policy does not create Joshternet participation.

A valid declaration does not override a robots restriction.

All supported version 1 declarations participate, including declarations where
the optional `josh` member is omitted, `true`, or `false`. Those values describe
identity as Undeclared, Affirmed, or Declined. `Declined` is still
participation.

## Crawl sources

JoshBot crawls an origin only when it is:

- an independently verified Joshternet participant; or
- explicitly configured by the operator as a curated seed; or
- a discovered crawl source admitted by the optional automatic
  expansion policy.

A curated seed grants permission to use that origin as a discovery source. It
does not assert that the origin participates in the Joshternet.

Removing a seed prevents seed status from making it crawl-eligible. Existing
verification observations and discovery provenance are not erased.

Automatic sources remain distinct from curated seeds and verified
participants. Automatic promotion records crawl eligibility only. It does not
assert trust, ownership, endorsement, identity, or Joshternet participation.

## How pages are crawled

Each crawl starts at the source origin’s root path:

```text
/
```

The root is depth `0`.

Same-origin pages use a deterministic breadth-first frontier.

A page is selected at most once during a crawl after URL normalization and
deduplication. Fragment-only differences do not create additional page work.

Eligible same-origin HTTP and HTTPS links may extend the page frontier.
External HTTP and HTTPS links contribute their canonical origins as
declaration-verification candidates.

External origins are not followed as pages during the current source crawl.
They become crawl-eligible only after independent verification, explicit
operator curation, or admission by the enabled automatic expansion policy.

Requests within one source crawl are sequential. The configured delay is
applied between requests, not after the final request.

JoshBot parses supported response content for links. It does not run a browser,
execute JavaScript, save screenshots, or render pages.

JoshBot does not resume an old in-memory frontier. If a crawl is interrupted,
the next eligible attempt begins at the root.

## Robots exclusion

Every requested URI is evaluated against the robots policy selected for:

```text
Joshternet-Joshbot
```

The robots resource is retrieved through the same guarded HTTP path used for
other crawler traffic.

A URI disallowed for JoshBot is not fetched.

Robots permission and Joshternet participation remain separate:

- robots policy grants or denies URI retrieval;
- a Joshternet declaration controls participation;
- an operator seed controls whether a non-participant may be used as a private
  discovery source.

A missing or invalid declaration does not prohibit crawling an explicit seed
when robots policy permits it.

## Network safety

Before connecting, JoshBot resolves destination addresses and requires every
resolved address to be public.

It rejects unsafe destinations including:

- loopback;
- private address space;
- link-local addresses;
- multicast;
- shared IPv4 space;
- unspecified addresses;
- other non-public ranges enforced by `internal/netguard`.

Redirect destinations pass through the same policy.

Candidate evidence is stored before automatic-admission network checks. When a
candidate enters a bounded admission batch, JoshBot performs a fresh DNS
resolution immediately before admission, requires every returned address to be
public, and connects only through validated IP literals. A transient
resolution failure is deferred; an unsafe result remains in private evidence
but is not admitted automatically.

Operators should still enforce outbound firewall policy. Application checks
are not a substitute for host-level network controls.

## Crawl limits

The runtime exposes these controls:

| Variable | Default | Meaning |
| --- | ---: | --- |
| `JOSHBOT_CRAWL_MAX_DEPTH` | `4` | Deepest same-origin page depth, with the root at zero |
| `JOSHBOT_CRAWL_MAX_PAGES` | `32` | Maximum frontier pages selected for guarded retrieval |
| `JOSHBOT_CRAWL_MAX_PAGE_BYTES` | `1048576` | Maximum accepted page body size |
| `JOSHBOT_CRAWL_REQUEST_DELAY` | `1s` | Delay between sequential page requests |
| `JOSHBOT_CRAWL_REDIRECT_LIMIT` | `5` | Maximum redirects for a page request |

Additional discovery controls are:

| Variable | Default | Meaning |
| --- | ---: | --- |
| `JOSHBOT_DISCOVERY_INTERVAL` | `168h` | Minimum interval between source attempts |
| `JOSHBOT_DISCOVERY_POLL_INTERVAL` | `30s` | Idle polling interval |
| `JOSHBOT_DISCOVERY_PAGE_TIMEOUT` | `30s` | Per-page operation timeout |
| `JOSHBOT_AUTOMATIC_CRAWL_ENABLED` | `false` | Admit discovered origins as private crawl sources |
| `JOSHBOT_AUTOMATIC_CRAWL_MAX_PENDING_PROBES` | `1000` | Pause automatic source claims at this pending-probe high-water mark |
| `JOSHBOT_AUTOMATIC_CRAWL_MAX_PROMOTIONS_PER_RUN` | `100` | Maximum newly promoted automatic sources in one durable crawl-run admission batch |
| `JOSHBOT_AUTOMATIC_CRAWL_EXCLUDED_HOSTS` | empty | Add emergency domain patterns beyond the durable operator-managed avoid list |

Go duration syntax is used. For example, one week is `168h`, not `7d`.

The durable domain avoid list starts with common hosted publishing and social
platforms and is editable through the operator control API. Exact domains include their
subdomains. A public-suffix family such as `blogspot.*` also matches regional
forms including `blogspot.co.uk` and `blogspot.no`. Avoided origins remain in
private discovery evidence but are not promoted or probed automatically.

`JOSHBOT_CRAWL_MAX_DEPTH` may be zero, limiting the crawl to its root page.

Maximum pages, maximum page bytes, and redirect limit must be positive.
Request delay may be zero.

Redirect hops for a selected page do not consume additional frontier-page
slots.

These values are operator safety limits, not Joshternet protocol rules.

When automatic expansion is enabled, safe canonical origins discovered through
eligible public links can enter deterministic, bounded admission batches.
Every candidate and provenance edge is durable before admission. Attribution
belongs to the first crawl run that discovered the link origin. Each admission
run allocates candidates in canonical-origin order up to that run's configured
promotion limit; overflow remains unbatched and available to a later run.
Canonical origin deduplication keeps repeated links from creating duplicate
sources.

Each allocated candidate receives at most three immediate resolution attempts
when the failure is transient. Capacity, policy, and transient-network
deferrals are durable outcomes and do not delete evidence. A candidate is
promoted only after fresh network validation, applicable avoid/block policy,
probe-queue capacity, and the run promotion budget all permit it.
Automatic expansion continues across crawl generations until the operator
disables it, blocks a source, pauses the service, or no eligible source is due.
When the pending verification probe queue reaches the configured high-water
mark, JoshBot temporarily delays automatically discovered sources while
retaining them for later. Curated seeds and verified participants remain
eligible, and automatic claims resume after the worker drains the probe queue.
This backpressure bounds active verification work without imposing a lifetime
limit on discovery.

Operators can inspect and control the private crawl-source inventory without
changing participation or deleting history:

```text
joshbot source list
joshbot source block https://example.org
joshbot source allow https://example.org
```

The list reports curated, automatically discovered, verified, and blocked
status independently. Blocking prevents future discovery claims even when an
origin is curated or verified. Allowing restores eligibility according to its
remaining classifications and the automatic-expansion configuration.

## Content handling

Retrieval and parsing are bounded.

Unsupported media types, invalid content, and oversized bodies do not extend
the same-origin page frontier.

The crawler canonicalizes external origins before storing or scheduling them.
Unsupported schemes, invalid URLs, and credential-bearing URLs are discarded.
Canonical HTTP and HTTPS link evidence can be retained before network
validation; unsafe destinations are rejected from automatic admission, not
silently removed from discovery history.

Links to configured shared-hosting and social platform suffixes remain in
candidate and edge history, but do not receive verification probes and do not
become automatic crawl sources. Matching includes the named host and its
subdomains. Curated seeds remain an explicit operator-controlled override.

## What JoshBot stores

JoshBot retains origin-level semantic and operational data:

- explicit seed status;
- automatic-source status;
- operator crawl-block status;
- source last-attempt time;
- source-to-candidate origin relationships;
- first and latest candidate discovery times;
- declaration observations;
- effective participant state;
- verification queue and lease state;
- durable verification queue transitions;
- bounded crawl-run summaries and stop reasons;
- sanitized requested and final page URLs without queries, fragments, or credentials;
- per-page HTTP status, duration, response size, media type, redirect count,
  robots decision, link totals, URLs found/enqueued, typed failure category,
  and bounded outcome;
- per-run admitted/deferred promotions, blocked/failed pages, URLs
  found/enqueued, and remaining frontier at stop;
- worker and discovery service heartbeats with idle, paused, failed,
  current-origin, and bounded-message state;
- persistent discovery and verification pause state;
- migration metadata.

This state supports retries, discovery provenance, politeness, and registry
construction.

## What JoshBot does not store

JoshBot does not retain:

- HTML archives;
- complete page bodies;
- complete declaration bodies;
- page titles;
- anchor text;
- screenshots;
- headers;
- cookies;
- query strings or fragments from page URLs;
- URL credentials;
- the in-memory frontier;
- arbitrary page content.

Operational crawl telemetry is retained for 30 days by default and is removed
on discovery service startup according to
`JOSHBOT_CRAWL_TELEMETRY_RETENTION`. Verification observations, queue history,
origin relationships, and participation state use their existing durable
retention rules.

Promotion telemetry is attributed to the first crawl run that discovered each
link candidate. `promotions_admitted` counts candidates eventually promoted
from that original evidence; `promotions_deferred` counts every other original
link candidate, including candidates beyond the bounded resolution batch and
terminal network or policy rejections. Later successful admission moves a
candidate from deferred to admitted without resolving an unbounded batch.

Private discovery state does not appear in the public registry.

## Retry behavior

Only transient failures are retried. The transient categories are `dns`,
`transport`, `timeout`, `robots_temporary`, `http_408`, `http_429`, `http_5xx`,
`declaration_unavailable`, `processor`, and `store`. Other typed categories,
including unsafe addresses, policy blocks, robots denials, malformed or
unsupported origins, unsupported or oversized content, and exhausted budgets,
are terminal for retry scheduling.

Declaration verification, the root page of a source crawl, and
automatic-admission DNS resolution make at most three immediate attempts in a
processing cycle. Non-root pages are attempted once. The durable
consecutive-failure schedule is `5m`, `30m`, `2h`, `12h`, then `24h` for each
later failure. Valid `Retry-After` values can extend verification and root-page
delays up to the same `24h` cap.

An exhausted three-attempt cycle is one logical queue claim, one root
frontier-page attempt, or one admission decision. Verification writes one
observation, crawling writes one page-attempt row, and admission writes one
batch outcome. The applicable durable failure streak increments once, not once
per physical network attempt.

A successful or terminal result clears the durable streak. If at least one
page parsed successfully, the crawl source is considered successful for retry
scheduling even when a later page failed; the failed page remains visible in
telemetry.

## How to control JoshBot

A site can control JoshBot through standard RFC 9309 `robots.txt` rules.

To disallow all JoshBot crawling:

```text
User-agent: Joshternet-Joshbot
Disallow: /
```

To disallow one path while allowing other eligible paths:

```text
User-agent: Joshternet-Joshbot
Disallow: /private/
```

Place the policy at:

```text
/robots.txt
```

JoshBot does not treat nonstandard robots directives as participation or
identity declarations.

A robots restriction controls future retrieval attempts. It does not erase
already retained origin-level verification or discovery metadata.

## Failure behavior

A failed, denied, or interrupted page does not make an origin trusted.

Context cancellation stops current work. A later eligible attempt restarts at
the source root.

Network, robots, parsing, and resource-limit failures remain bounded and do
not bypass independent declaration verification.

## Reporting a crawler problem

For unexpected requests, robots behavior, or traffic, open a
[crawler behavior report](https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml).

Reports are welcome even when your robots configuration appears correct.
Please include the affected origin, approximate request time, requested path,
observed User-Agent, relevant public robots rules, and the behavior that
appeared wrong.

The issue is public. Do not publish credentials, private server logs, access
tokens, private keys, sensitive infrastructure details, or personal
information.

If the report would disclose a security vulnerability, use
[GitHub Private Vulnerability Reporting](https://github.com/joshternet/joshbot/security/advisories/new)
instead.

## Operator responsibility

Only add a curated seed when you intend JoshBot to crawl that origin.

Before deployment, operators should review:

- seed selection;
- crawl depth;
- page budget;
- response-size limit;
- request delay;
- discovery interval;
- outbound firewall policy;
- applicable legal and operational requirements.

Deployment commands and configuration are documented in the
[Deployment guide](../deploy/README.md).
