## Summary

Describe what changed and why.

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

## Validation

List the exact commands you ran and their results.

## Checklist

- [ ] I kept the change focused.
- [ ] I added or updated tests for production behavior.
- [ ] I ran the complete PostgreSQL-backed test suite.
- [ ] I ran race, repeatability, and coverage checks.
- [ ] Total statement coverage remains exactly `100.0%`.
- [ ] I ran formatting, module, vet, and diff checks.
- [ ] I ran both discovery fuzz targets when relevant.
- [ ] I ran deployment and recovery checks when relevant.
- [ ] I did not modify an applied migration.
- [ ] I did not unintentionally change public registry output.
- [ ] I did not include credentials, private data, generated runtime state, or local configuration.
- [ ] I updated documentation for user-visible or operator-visible changes.