# Contributing to JoshBot

Thanks for helping improve JoshBot.

JoshBot processes untrusted network input, controls PostgreSQL state, and
publishes public data. Correctness and safety matter more than adding behavior
quickly.

You do not need to be named Josh. Useful tests and careful reasoning count for
more around here.

## Before changing code

Search existing issues before opening a new one.

Use:

- the [bug report form](https://github.com/joshternet/joshbot/issues/new?template=bug_report.yml)
  for reproducible software defects;
- the [crawler behavior report](https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml)
  for unexpected crawling, traffic, or robots behavior;
- the [feature request form](https://github.com/joshternet/joshbot/issues/new?template=feature_request.yml)
  for proposed implementation behavior;
- GitHub Private Vulnerability Reporting for security problems;
- the conduct report form for Code of Conduct concerns.

Do not include credentials, private data, sensitive infrastructure details, or
unredacted production logs in an issue or pull request.

Understand which repository owns the proposed change:

- JoshBot implementation changes belong in this repository.
- Joshternet protocol and RFC changes belong in the
  [Joshternet specification repository](https://github.com/joshternet/spec).

Do not silently change Joshternet declaration or participation semantics inside
crawler implementation code.

Substantial behavior, schema, compatibility, protocol, or security changes
should be discussed in an issue before implementation.

## Development setup

You need:

- Go 1.27;
- PostgreSQL 18 for database integration tests;
- Git;
- a POSIX shell.

Docker Engine and Docker Compose v2 are required for deployment, publication,
backup, and recovery testing.

Build the command:

```bash
go build ./cmd/joshbot
```

Run tests without forcing database integration:

```bash
go test ./...
```

Database-backed tests skip when `JOSHBOT_TEST_DATABASE_URL` is not configured.
Setting `JOSHBOT_REQUIRE_DATABASE_TESTS=1` changes missing database
configuration into a test failure. Release validation uses the required mode.

## Database tests

Create a disposable PostgreSQL database. Never use a production database.

A local Unix-socket configuration is:

```bash
export JOSHBOT_TEST_DATABASE_URL="postgres://$(whoami)@localhost:55432/joshbot_test?host=%2Ftmp&sslmode=disable"
export JOSHBOT_REQUIRE_DATABASE_TESTS=1
```

The test database URL must not contain a password. Use local PostgreSQL
authentication or the supported separate password mechanism.

Each store test creates and removes its own schema inside the configured test
database.

## Test-driven changes

For new production behavior:

1. Define the smallest observable behavior.
2. Write a focused failing test.
3. Run the focused test and confirm the expected failure.
4. Add the minimum production implementation.
5. Run the focused test and confirm it passes.
6. Run the complete suite.
7. Refactor only when the passing tests justify it.
8. Run the complete suite again after refactoring.

Security regressions and fuzz discoveries need permanent regression tests.

Tests should describe behavior rather than implementation history.

## Complete test suite

Run the complete PostgreSQL-backed suite:

```bash
JOSHBOT_REQUIRE_DATABASE_TESTS=1 \
go test -count=1 ./...
```

Run race, repeatability, and coverage:

```bash
JOSHBOT_REQUIRE_DATABASE_TESTS=1 \
go test \
  -count=10 \
  -race \
  -coverprofile=/tmp/joshbot-coverage.out \
  ./...

go tool cover \
  -func=/tmp/joshbot-coverage.out
```

Total statement coverage must remain exactly `100.0%`.

Coverage is a floor for exercised behavior, not a reason to write assertions
that prove nothing. Tests must still validate meaningful outcomes, failure
handling, safety boundaries, and invariants.

Run formatting, module, vet, diff, and release checks:

```bash
gofmt -l .
go mod tidy -diff
go vet ./...
git diff --check
./scripts/release-check.sh
```

A successful `gofmt -l .` and `go mod tidy -diff` produce no output.

## Fuzzing

Run the origin parser:

```bash
go test \
  ./internal/origin \
  -run '^$' \
  -fuzz '^FuzzParseRoundTrip$' \
  -fuzztime=10s
```

Run declaration parsing:

```bash
go test \
  ./internal/declaration \
  -run '^$' \
  -fuzz '^FuzzParseDeclaration$' \
  -fuzztime=10s
```

Run discovery extraction:

```bash
go test \
  ./internal/discovery \
  -run '^$' \
  -fuzz '^FuzzExtract$' \
  -fuzztime=10s

go test \
  ./internal/discovery \
  -run '^$' \
  -fuzz '^FuzzExtractPageLinks$' \
  -fuzztime=10s
```

Run robots parsing and evaluation:

```bash
go test \
  ./internal/robots \
  -run '^$' \
  -fuzz '^FuzzParseAndEvaluate$' \
  -fuzztime=10s
```

A fuzz input that exposes a defect must remain in the saved corpus as a
regression after the defect is fixed.

## Golden fixtures

Golden files protect deterministic public output and other byte-level
contracts.

Normal tests compare generated bytes without modifying fixtures.

Only the exact value `JOSHBOT_UPDATE_GOLDEN=1` enables fixture updates:

```bash
JOSHBOT_UPDATE_GOLDEN=1 \
go test ./internal/publicdata
```

Use update mode only for an intentional compatibility change. Inspect the
entire fixture diff, explain the change in the pull request, and rerun the test
without update mode.

Never update a golden merely to make an unexplained failure pass.

## Deployment checks

For deployment, publication, backup, recovery, Compose, or credential-boundary
changes, run:

```bash
docker compose \
  --env-file deploy/.env.example \
  config \
  --quiet

./deploy/publish_test.sh
./deploy/smoke.sh
```

These checks exercise container isolation, deterministic export, publication,
PostgreSQL authorization, backup, restore, and application recovery.

## Dependencies

JoshBot is standard-library-first, not standard-library-only.

Current direct dependencies include PostgreSQL support through `pgx` and
network text processing through `golang.org/x/net`.

A new dependency needs a clear justification covering:

- why the standard library or existing dependencies are insufficient;
- maintenance and release activity;
- security impact;
- license compatibility;
- binary and operational cost.

Run:

```bash
go mod tidy -diff
go mod verify
```

Do not upgrade unrelated dependencies inside a focused behavior change.

## Security-sensitive changes

All production network retrieval must remain under both the network guard and
robots enforcement.

Do not introduce:

- `http.Get` for production crawler traffic;
- an unguarded transport;
- redirect handling that bypasses address validation;
- automatic proxy behavior that escapes the configured network boundary;
- credentials in URLs, logs, command arguments, fixtures, or public output;
- publication credentials in database-connected containers;
- database credentials in the publisher.

Database mutations must preserve transactional authority, lease ownership, and
the established role boundaries.

Security testing must use local, fake, or explicitly authorized
infrastructure. Do not test JoshBot vulnerabilities against third-party sites
without permission.

## Database migrations

Migrations are embedded from `internal/store/migrations`.

Never edit an applied migration. Add a new forward migration instead.

Migration changes must test:

- installation from an empty database;
- upgrade from the previous schema;
- required constraints and indexes;
- least-privilege runtime access;
- expected migration recording.

## Public registry compatibility

Registry output is a public compatibility boundary.

Do not change registry JSON, file paths, ordering, or golden fixtures casually.
Any intentional compatibility change must be explained in its issue and pull
request.

Private crawl seeds, queue state, discovery timing, candidate provenance,
leases, and internal crawl details must not leak into public output.

## Pull requests

Keep pull requests small and coherent.

Explain:

- what changed;
- why it changed;
- security or compatibility consequences;
- exact validation commands and results;
- any operator action required.

Do not mix unrelated formatting or cleanup into a behavior change.

Before committing, inspect:

```bash
git diff --check
git status --short
git diff --cached
```

Never commit secrets, tokens, passwords, generated database files, backup
archives, coverage files, or local environment configuration.

## Security

Do not open a public issue for an undisclosed vulnerability. Follow
[SECURITY.md](SECURITY.md).

## Community expectations

Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

Technical disagreement is welcome. Personal attacks are not. Nobody gains
special contributor standing by having a particular name.