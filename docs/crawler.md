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
- explicitly configured by the operator as a curated seed.

A curated seed grants permission to use that origin as a discovery source. It
does not assert that the origin participates in the Joshternet.

Removing a seed prevents seed status from making it crawl-eligible. Existing
verification observations and discovery provenance are not erased.

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
They become crawl-eligible only after independent verification or explicit
operator curation.

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

Go duration syntax is used. For example, one week is `168h`, not `7d`.

`JOSHBOT_CRAWL_MAX_DEPTH` may be zero, limiting the crawl to its root page.

Maximum pages, maximum page bytes, and redirect limit must be positive.
Request delay may be zero.

Redirect hops for a selected page do not consume additional frontier-page
slots.

These values are operator safety limits, not Joshternet protocol rules.

## Content handling

Retrieval and parsing are bounded.

Unsupported media types, invalid content, and oversized bodies do not become
page frontiers.

The crawler canonicalizes external origins before storing or scheduling them.
Unsupported schemes, invalid URLs, credential-bearing URLs, unsafe
destinations, and links outside configured limits are rejected.

## What JoshBot stores

JoshBot retains origin-level semantic and operational data:

- explicit seed status;
- source last-attempt time;
- source-to-candidate origin relationships;
- first and latest candidate discovery times;
- declaration observations;
- effective participant state;
- verification queue and lease state;
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
- internal page history;
- page depth;
- the in-memory frontier;
- arbitrary page content.

Private discovery state does not appear in the public registry.

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