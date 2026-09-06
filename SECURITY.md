# Security Policy

JoshBot crawls untrusted web origins, connects to PostgreSQL, and can publish
registry data through the GitHub API. Please report vulnerabilities privately.

## Supported versions

| Version | Supported |
| --- | --- |
| 1.0.x | Yes |
| Earlier versions | No |

Security fixes are made against the latest supported release and the current
default branch.

## Report a vulnerability

Use
[GitHub Private Vulnerability Reporting](https://github.com/joshternet/joshbot/security/advisories/new).

Do not open a public issue, pull request, or discussion for an undisclosed
vulnerability.

A useful report includes:

- the affected version or commit;
- the affected component;
- clear reproduction steps;
- expected and observed behavior;
- realistic security impact;
- relevant logs with credentials and private information removed;
- a suggested correction, if known.

Never include live passwords, access tokens, private keys, database contents,
or unrelated personal information.

## Response and disclosure

Security reports will be reviewed as time permits. Confirmation, severity,
remediation, and release timing depend on reproducibility and impact.

Please allow time to investigate and prepare a fix before public disclosure.
A report may be closed if it is not reproducible, is outside JoshBot’s threat
model, or describes intended behavior without a security boundary failure.

## Important security boundaries

Reports are especially useful when they demonstrate a failure in:

- canonical origin validation;
- DNS or IP-address safety checks;
- redirect validation;
- robots enforcement;
- response-size or crawl-budget enforcement;
- PostgreSQL authorization or lease ownership;
- secret isolation;
- deterministic public-data projection;
- GitHub publication target validation;
- exclusion of private operational data from public output.

## Operational security

Deployment mistakes are not automatically JoshBot vulnerabilities. Operators
are responsible for:

- host firewall and Docker networking;
- filesystem ownership and permissions;
- database and GitHub credentials;
- backup storage and retention;
- publication-repository permissions;
- schedules and crawl seeds;
- timely updates and supported releases.

Operational guidance is available in
[deploy/README.md](deploy/README.md).