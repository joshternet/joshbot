# JoshBot

The Joshternet discovery and registry crawler.

## Development requirements

JoshBot requires Go 1.27.

The Phase 0 development foundation uses only the Go standard library.

## Quality checks

Run the normal test suite:

```bash
go test ./...
```

Run the race detector:

```bash
go test -race ./...
```

Run static analysis:

```bash
go vet ./...
```

Verify deterministic repeatability:

```bash
go test -count=10 ./...
```

## Coverage

All coverable Go statements must remain at 100% coverage.

Generate and inspect a coverage profile outside the repository:

```bash
go test -coverprofile=/tmp/joshbot-coverage.out ./...
go tool cover -func=/tmp/joshbot-coverage.out
```

The reported total must be `100.0%`.

## Continuous integration

The `Quality` GitHub Actions workflow runs for every pull request and every push
to `main`.

It enforces:

- Go formatting;
- module tidiness;
- `go vet`;
- normal tests;
- machine-readable test results;
- ten deterministic race-enabled test runs;
- exactly 100% statement coverage;
- a clean working tree after normal quality checks.

Each workflow run publishes a GitHub job summary and a downloadable quality
reports artifact containing:

- `quality-summary.md`;
- `formatting.txt`;
- `module-tidiness.txt`;
- `vet.txt`;
- `normal-tests.txt`;
- `test-results.json`;
- `race-tests.txt`;
- `coverage.out`;
- `coverage.txt`;
- `coverage.html`;
- `working-tree.txt`.

Reports are retained for 14 days. The job summary contains a direct link to the
artifact.

The workflow uses read-only repository permissions and immutable commit pins
for every external action.

## Golden-master tests

Golden files are committed fixtures stored beneath the conventional `testdata`
directory.

Golden-test support lives in `internal/testutil` so tests in multiple packages
can reuse it without adding test-only machinery to production packages.

The golden-test contract is:

- Actual output must be deterministic.
- Normal tests may read golden files but must never rewrite them.
- A missing fixture fails during a normal test and is not silently created.
- A mismatch identifies the fixture and reports expected and actual content.
- Golden updates require explicit developer intent.
- Updating a fixture is a reviewable behavior change, not a way to silence a
  failing test.

Normal comparison is the default:

```bash
go test ./...
```

Intentionally update golden files across all repository packages with:

```bash
JOSHBOT_UPDATE_GOLDEN=1 go test ./...
```

Only the exact value `1` enables updates. The environment variable is inherited
by every package test binary created by `go test ./...`, so packages do not need
to register duplicate test flags.

Never set `JOSHBOT_UPDATE_GOLDEN` in CI. After an intentional update, review
every fixture change:

```bash
git status --short
git diff -- ':(glob)**/testdata/**'
```

Tests must not depend on public Internet access, wall-clock timing, local
timezones, usernames, checkout paths, random map iteration, external services, or mutable remote state.
