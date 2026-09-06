# JoshBot

JoshBot is the discovery, verification, and public registry crawler for the
[Joshternet](https://joshternet.org).

It finds public web origins, verifies their Joshternet declarations, remembers
operational state in PostgreSQL, and produces a deterministic public registry.
Publication to GitHub happens through a separate, credential-isolated runtime.

The initial supported release is `v1.0.0`.

## What is the Joshternet?

The Joshternet is an opt-in web network defined by the
[Joshternet specifications](https://github.com/joshternet/spec).

A site participates by publishing a valid declaration at:

```text
/.well-known/josh
```

JoshBot implements discovery, verification, and registry publication. It does
not define the protocol by itself, and an implementation change in this
repository must not silently redefine a Joshternet specification.

## What JoshBot does

JoshBot:

- crawls independently verified origins and operator-curated seeds;
- discovers external web origins through public links;
- verifies each candidate’s Joshternet declaration independently;
- remembers verification, scheduling, and discovery state in PostgreSQL;
- exports deterministic public registry files;
- publishes exact registry trees through the GitHub API;
- avoids creating publication commits when the registry has not changed.

The short version is that links create candidates, while valid declarations
create participants.

## How it works

```text
eligible crawl source
        |
        v
same-origin pages within crawl limits
        |
        v
external public link
        |
        v
canonical candidate origin
        |
        v
GET /.well-known/josh
        |
        v
valid version 1 declaration?
        |
        v
participant in deterministic public registry
```

Same-origin links may extend the current page frontier. External links do not:
their origins become verification candidates.

A candidate is not trusted merely because another site linked to it. Every
candidate must publish and pass its own declaration verification before it can
appear in the public registry.

## Participation

JoshBot recognizes these version 1 declarations:

```json
{"version":1}
```

```json
{"version":1,"josh":true}
```

```json
{"version":1,"josh":false}
```

All three are valid participation declarations.

The optional `josh` member describes identity:

| Declaration | Identity state |
| --- | --- |
| `{"version":1}` | Undeclared |
| `{"version":1,"josh":true}` | Affirmed |
| `{"version":1,"josh":false}` | Declined |

`Declined` does not mean nonparticipating. It means the origin participates
while explicitly declining the Josh identity assertion.

Likewise, `Undeclared` means the declaration does not make that identity
assertion. It remains a valid participation declaration.

Robots permission and Joshternet participation are separate. A declaration
does not override `robots.txt`, and `robots.txt` does not create or remove a
Joshternet declaration.

## Crawling

JoshBot can crawl two kinds of sources:

- independently verified Joshternet participants;
- origins explicitly configured by an operator as curated seeds.

A curated seed grants permission to use an origin as a private discovery
source. It does not declare that the origin participates in the Joshternet and
does not place it in the public registry.

Each crawl:

- starts at the source origin’s root path;
- follows eligible same-origin links breadth-first;
- stays within configured page, depth, response-size, redirect, and delay
  limits;
- evaluates robots permission for each requested URI;
- sends requests through the network safety guard;
- processes requests sequentially for that source;
- turns external HTTP and HTTPS links into candidate origins.

External candidates are not recursively crawled merely because they were
discovered. They become eligible sources only after independent verification
or explicit operator curation.

See [Crawler behavior](docs/crawler.md) for the detailed site-owner and
operator contract.

## Network and security model

JoshBot processes untrusted URLs and web content. Its safety boundaries
include:

- strict origin parsing and canonicalization;
- DNS resolution before connection;
- rejection of loopback, private, link-local, multicast, shared, unspecified,
  and other non-public addresses;
- validation of every resolved address;
- guarded redirects;
- robots-aware HTTP retrieval;
- response-size and crawl-budget limits;
- PostgreSQL leases and transactional completion;
- separate database and publication credentials;
- a publisher with no database access;
- deterministic output with no private discovery state.

These controls reduce risk but do not make arbitrary crawling risk-free.
Operators remain responsible for host networking, firewall policy,
credentials, storage, schedules, legal requirements, and the origins they add
as curated seeds.

## Data JoshBot keeps

JoshBot stores origin-level semantic and operational data, including:

- canonical origins;
- declaration verification observations;
- effective participant state;
- first and latest observation times;
- verification queue state;
- lease ownership and expiration;
- curated seed status;
- discovery-source attempt times;
- source-to-candidate origin relationships;
- first and latest candidate discovery times;
- applied database migration metadata.

This state supports verification, retries, politeness, discovery provenance,
and deterministic registry construction.

## Data JoshBot does not keep

JoshBot does not operate as a page archive.

It does not retain:

- HTML archives;
- complete page or declaration response bodies;
- page titles;
- anchor text;
- screenshots;
- cookies;
- response headers;
- internal page history;
- page depth;
- the in-memory crawl frontier;
- arbitrary page content.

Private crawl seeds, queue state, discovery timing, leases, and candidate
provenance do not appear in the public registry.

## Public registry

A database-connected runtime projects verified participant state into a
deterministic directory containing `registry.json` and per-origin node files.

Equivalent participant state produces byte-identical output. File paths and
ordering are deterministic.

Publication occurs separately:

1. The tools runtime exports the snapshot without GitHub credentials.
2. The publisher receives a read-only snapshot and GitHub credentials.
3. The publisher verifies the machine-managed target branch and sentinel.
4. It creates the exact desired Git tree.
5. It updates the branch without force.
6. If the exact tree already exists, it creates no commit.

The JoshBot registry format is an implementation compatibility boundary. It is
not itself a Joshternet RFC.

## Crawler identity

JoshBot uses this exact HTTP User-Agent:

```text
Joshternet-Joshbot (+https://joshternet.org/joshbot)
```

The public crawler information page is:

```text
https://joshternet.org/joshbot
```

To block JoshBot completely, a site can publish:

```text
User-agent: Joshternet-Joshbot
Disallow: /
```

For unexpected crawling, robots behavior, or traffic, open a
[crawler behavior report](https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml).

Crawler reports are welcome even when the site’s robots configuration appears
correct. Do not include credentials, private server logs, or sensitive
infrastructure details in a public issue.

Security vulnerabilities must be reported privately under the
[Security policy](SECURITY.md).

## Requirements

Development requires:

- Go 1.27;
- Git;
- a POSIX shell;
- PostgreSQL 18 for database integration tests.

Deployment additionally requires Docker Engine and Docker Compose v2.

## Getting started

Build JoshBot:

```bash
go build ./cmd/joshbot
```

Show the current command reference:

```bash
./joshbot help
```

Run tests that do not require a configured database:

```bash
go test ./...
```

For the complete PostgreSQL-backed suite, configure
`JOSHBOT_TEST_DATABASE_URL` as described in
[Contributing](CONTRIBUTING.md).

Production deployment, backup, recovery, and publication are documented in
the [Deployment guide](deploy/README.md).

## Commands

```text
joshbot health
joshbot migrate
joshbot schedule <origin>
joshbot seed add <origin>
joshbot seed remove <origin>
joshbot seed list
joshbot worker
joshbot discover [--once]
joshbot export --output <directory>
joshbot publish --input <directory>
joshbot help
```

### `health`

Checks PostgreSQL connectivity.

```bash
joshbot health
```

### `migrate`

Applies pending embedded database migrations.

```bash
joshbot migrate
```

Applied migrations are immutable. Schema changes require a new forward
migration.

### `schedule`

Schedules an origin for recurring declaration verification.

```bash
joshbot schedule https://example.com
```

Input paths, queries, and fragments are reduced to the canonical origin.

### `seed`

Manages operator-curated crawl seeds.

```bash
joshbot seed add https://directory.example
joshbot seed remove https://directory.example
joshbot seed list
```

A seed creates a private discovery source. It is not a declaration,
verification result, or public registry entry.

### `worker`

Runs the long-lived declaration-verification worker.

```bash
joshbot worker
```

### `discover`

Runs the discovery service.

```bash
joshbot discover
```

Run one discovery attempt:

```bash
joshbot discover --once
```

### `export`

Writes a deterministic public registry snapshot. The destination directory
must not already exist.

```bash
joshbot export --output <directory>
```

### `publish`

Publishes an existing registry snapshot to the configured GitHub repository.

```bash
joshbot publish --input <directory>
```

The normal deployment uses `deploy/publish.sh` so database export and GitHub
publication occur in separate containers.

### `help`

Prints the command reference.

```bash
joshbot help
```

## Development

The project uses test-driven changes, PostgreSQL integration testing, race and
repeatability checks, fuzzing, deployment validation, and exactly `100.0%`
statement coverage.

Read [Contributing](CONTRIBUTING.md) for the complete development workflow.

## Specifications

Joshternet protocol semantics belong to the
[Joshternet specification repository](https://github.com/joshternet/spec).

Implementation changes belong here. Protocol changes belong there.

## Documentation

- [Architecture](docs/architecture.md)
- [Crawler behavior](docs/crawler.md)
- [Deployment guide](deploy/README.md)
- [Changelog](CHANGELOG.md)

## Contributing

Contributions are welcome. You do not have to be named Josh; that would be an
oddly specific dependency.

Read [Contributing](CONTRIBUTING.md) before opening an issue or pull request.

For reproducible software defects, use the
[bug report form](https://github.com/joshternet/joshbot/issues/new?template=bug_report.yml).

For crawler-specific concerns, use the
[crawler behavior report](https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml).

## Community

Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Do not disclose an unpatched vulnerability through a public issue. Follow the
[Security policy](SECURITY.md) and use GitHub Private Vulnerability Reporting.

## License

JoshBot is licensed under [BSD-3-Clause](LICENSE).

Copyright belongs to Joshua Morris. Source and binary redistributions must
retain the copyright notice, license conditions, and warranty disclaimer.