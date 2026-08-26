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

## Golden-master tests

Golden files are committed fixtures stored beneath the conventional `testdata`
directory.

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

Intentionally update golden files with:

```bash
go test ./... -update
```

Never use `-update` in CI. After an intentional update, review every fixture
change:

```bash
git status --short
git diff -- testdata
```

Tests must not depend on public Internet access, wall-clock timing, local
timezones, usernames, checkout paths, random map iteration, external services, or mutable remote state.
