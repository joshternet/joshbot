## Summary

Describe what changed and why.

## Goal and constraints

- Inputs / outputs:
- Error cases:
- Performance / UX targets:
- Acceptance criteria:
- Do not modify:

## Security and compatibility

Describe any effect on:

- network safety;
- robots behavior;
- declaration verification;
- PostgreSQL schema or authorization;
- queue and lease behavior;
- crawl privacy or resource limits;
- public registry compatibility;
- GitHub publication;
- deployment or recovery.

Write “None” when there is no effect.

## AAA checklist

- [ ] **Reliability:** No known P0/P1 issues; graceful failure paths; meets service SLOs.
- [ ] **Performance:** Meets defined performance budgets.
- [ ] **Accessibility:** WCAG 2.2 AA where UI applies; keyboard and screen-reader support.
- [ ] **Security:** Least privilege; secrets handling; privacy by design.
- [ ] **Observability:** Metrics, logs, or alerts updated when behavior changes.
- [ ] **Polish:** Consistent UX; empty/error/loading states where applicable.
- [ ] **Readiness:** Tests, docs, and release notes included; rollback considered.

## Validation

List the exact commands you ran and their results.

## Checklist

- [ ] I kept the change focused.
- [ ] I added or updated tests for production behavior.
- [ ] I ran the complete PostgreSQL-backed test suite.
- [ ] I ran race, repeatability, and coverage checks.
- [ ] Total statement coverage remains exactly `100.0%`.
- [ ] I ran formatting, module, vet, and diff checks.
- [ ] I ran the relevant fuzz targets when relevant (`FuzzParseRoundTrip`, `FuzzParseDeclaration`, `FuzzExtractPageLinks`, `FuzzParseAndEvaluate`).
- [ ] I ran deployment and recovery checks when relevant.
- [ ] I did not modify an applied migration.
- [ ] I did not unintentionally change public registry output.
- [ ] I did not include credentials, private data, generated runtime state, local configuration, or private infrastructure details.
- [ ] I updated documentation for user-visible or operator-visible changes.
