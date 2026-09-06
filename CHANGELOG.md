# Changelog

All notable JoshBot changes are documented here.

The format follows Keep a Changelog, and releases use semantic versioning.

## [Unreleased]

No unreleased changes.

## [1.0.0] - 2026-09-05

### Added

- Canonical HTTP and HTTPS origin parsing.
- Public-address validation and guarded dialing.
- JoshBot HTTP identity and robots exclusion enforcement.
- Joshternet declaration parsing and verification.
- PostgreSQL verification history and effective origin state.
- A durable work queue with leasing, renewal, rescheduling, and transactional
  completion.
- Deterministic discovery-source scheduling and candidate provenance.
- Multi-page same-origin crawling with bounded breadth-first traversal.
- Operator-curated private crawl seeds.
- Deterministic public registry projection and directory snapshots.
- Atomic GitHub publication with unchanged-tree detection.
- Containerized verification, discovery, migration, export, publication,
  backup, restore, and recovery workflows.
- Exact statement-coverage, race, repeatability, fuzzing, formatting, module,
  vet, deployment, and recovery quality gates.

### Security

- Network destinations are resolved and validated before dialing.
- Unsafe address ranges and unsafe redirect destinations are rejected.
- Database credentials and GitHub publication credentials are isolated in
  separate containers.
- The public registry excludes private discovery and queue state.

[Unreleased]: https://github.com/joshternet/joshbot/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/joshternet/joshbot/releases/tag/v1.0.0