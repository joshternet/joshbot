#!/bin/sh

set -eu

script_directory=$(
	CDPATH= cd -- "$(dirname -- "$0")" &&
	pwd
)
repository_root=$(
	CDPATH= cd -- "$script_directory/.." &&
	pwd
)

cd "$repository_root"

failures=0

pass() {
	printf 'PASS: %s\n' "$1"
}

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	failures=$((failures + 1))
}

require_file() {
	path=$1

	if [ ! -f "$path" ]; then
		fail "required file is missing: $path"
		return
	fi

	if [ ! -s "$path" ]; then
		fail "required file is empty: $path"
		return
	fi

	pass "required file exists: $path"
}

require_executable() {
	path=$1

	if [ ! -x "$path" ]; then
		fail "required executable bit is missing: $path"
		return
	fi

	pass "required file is executable: $path"
}

require_text() {
	path=$1
	expected=$2
	description=$3

	if [ ! -f "$path" ]; then
		return
	fi

	if grep -Fq -- "$expected" "$path"; then
		pass "$description"
		return
	fi

	fail "$description"
}

reject_text() {
	path=$1
	rejected=$2
	description=$3

	if [ ! -f "$path" ]; then
		return
	fi

	if grep -Fiq -- "$rejected" "$path"; then
		fail "$description"
		return
	fi

	pass "$description"
}

scan_release_files() {
	scan_pattern=$1

	find . \
		-type f \
		! -path './.git/*' \
		! -path './external/*' \
		! -path './testdata/fuzz/*' \
		! -path './scripts/release-check.sh' \
		! -path './cmd/joshbot/documentation_test.go' \
		\( \
			-name '*.go' \
			-o -name '*.md' \
			-o -name '*.yml' \
			-o -name '*.yaml' \
			-o -name '*.sh' \
			-o -name '*.example' \
			-o -name '*.toml' \
			-o -name '*.txt' \
			-o -name 'Dockerfile' \
			-o -name 'compose.yaml' \
		\) \
		-print |
	while IFS= read -r scan_path; do
		scan_matches=$(
			grep \
				-niE \
				"$scan_pattern" \
				"$scan_path" \
				2>/dev/null ||
			true
		)

		if [ -n "$scan_matches" ]; then
			printf '%s\n' "$scan_matches" |
				sed "s#^#$scan_path:#"
		fi
	done
}

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	printf 'FAIL: release check must run inside a Git repository\n' >&2
	exit 1
fi

actual_root=$(git rev-parse --show-toplevel)

if [ "$actual_root" != "$repository_root" ]; then
	fail "script location does not resolve to the Git repository root"
else
	pass "release check resolved the repository root"
fi

required_files='
LICENSE
README.md
CHANGELOG.md
CONTRIBUTING.md
CODE_OF_CONDUCT.md
SECURITY.md
docs/architecture.md
docs/crawler.md
deploy/README.md
deploy/.env.example
.github/ISSUE_TEMPLATE/bug_report.yml
.github/ISSUE_TEMPLATE/feature_request.yml
.github/ISSUE_TEMPLATE/crawler_report.yml
.github/ISSUE_TEMPLATE/conduct_report.yml
.github/ISSUE_TEMPLATE/config.yml
.github/pull_request_template.md
.github/workflows/quality.yml
scripts/release-check.sh
'

for path in $required_files; do
	require_file "$path"
done

required_executables='
deploy/backup.sh
deploy/postgres/init/010-joshbot-roles.sh
deploy/publish.sh
deploy/publish_test.sh
deploy/smoke.sh
scripts/release-check.sh
'

for path in $required_executables; do
	require_executable "$path"
done

shell_files='
deploy/backup.sh
deploy/postgres/init/010-joshbot-roles.sh
deploy/publish.sh
deploy/publish_test.sh
deploy/smoke.sh
scripts/release-check.sh
'

for path in $shell_files; do
	if [ ! -f "$path" ]; then
		continue
	fi

	if sh -n "$path"; then
		pass "shell syntax is valid: $path"
	else
		fail "shell syntax is invalid: $path"
	fi
done

require_text \
	.github/workflows/quality.yml \
	'./scripts/release-check.sh' \
	'Quality workflow runs release readiness checks'

require_text \
	LICENSE \
	'BSD 3-Clause License' \
	'LICENSE identifies BSD-3-Clause'

require_text \
	LICENSE \
	'Copyright (c) 2026 Joshua Morris' \
	'LICENSE credits Joshua Morris'

require_text \
	LICENSE \
	'THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"' \
	'LICENSE contains the warranty disclaimer'

require_text \
	README.md \
	'# JoshBot' \
	'README identifies the project'

require_text \
	README.md \
	'Joshternet-Joshbot (+https://joshternet.org/joshbot)' \
	'README documents the exact crawler user agent'

require_text \
	README.md \
	'[Deployment guide](deploy/README.md)' \
	'README links to the deployment guide'

require_text \
	README.md \
	'[Crawler behavior](docs/crawler.md)' \
	'README links to the crawler documentation'

require_text \
	README.md \
	'[Architecture](docs/architecture.md)' \
	'README links to the architecture documentation'

require_text \
	README.md \
	'[Contributing](CONTRIBUTING.md)' \
	'README links to the contribution guide'

require_text \
	README.md \
	'[Security policy](SECURITY.md)' \
	'README links to the security policy'

require_text \
	README.md \
	'[Code of Conduct](CODE_OF_CONDUCT.md)' \
	'README links to the Code of Conduct'

require_text \
	README.md \
	'[Changelog](CHANGELOG.md)' \
	'README links to the changelog'

require_text \
	README.md \
	'[BSD-3-Clause](LICENSE)' \
	'README links to the license'

require_text \
	README.md \
	'https://github.com/joshternet/spec' \
	'README links to the Joshternet specification repository'

require_text \
	README.md \
	'https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml' \
	'README links to the crawler behavior report'

require_text \
	README.md \
	'{"version":1,"josh":false}' \
	'README documents declined participation'

while IFS= read -r command; do
	if [ -z "$command" ]; then
		continue
	fi

	require_text \
		README.md \
		"$command" \
		"README documents command: $command"
done <<'EOF'
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
EOF

require_text \
	CONTRIBUTING.md \
	'https://github.com/joshternet/spec' \
	'contribution guide directs protocol changes to the specification repository'

require_text \
	CONTRIBUTING.md \
	'https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml' \
	'contribution guide links to the crawler behavior report'

require_text \
	CONTRIBUTING.md \
	'JOSHBOT_UPDATE_GOLDEN=1' \
	'contribution guide documents intentional golden updates'

require_text \
	CONTRIBUTING.md \
	'100.0%' \
	'contribution guide documents the coverage requirement'

require_text \
	docs/crawler.md \
	'Joshternet-Joshbot (+https://joshternet.org/joshbot)' \
	'crawler documentation uses the exact user agent'

require_text \
	docs/crawler.md \
	'User-agent: Joshternet-Joshbot' \
	'crawler documentation includes the JoshBot robots product token'

require_text \
	docs/crawler.md \
	'Disallow: /' \
	'crawler documentation includes a complete opt-out example'

require_text \
	docs/crawler.md \
	'https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml' \
	'crawler documentation links to the crawler behavior report'

require_text \
	docs/crawler.md \
	'JOSHBOT_CRAWL_MAX_DEPTH' \
	'crawler documentation covers maximum depth'

require_text \
	docs/crawler.md \
	'JOSHBOT_CRAWL_MAX_PAGES' \
	'crawler documentation covers the page budget'

require_text \
	docs/crawler.md \
	'JOSHBOT_CRAWL_MAX_PAGE_BYTES' \
	'crawler documentation covers the response-size limit'

require_text \
	docs/crawler.md \
	'JOSHBOT_CRAWL_REQUEST_DELAY' \
	'crawler documentation covers request delay'

require_text \
	docs/crawler.md \
	'JOSHBOT_CRAWL_REDIRECT_LIMIT' \
	'crawler documentation covers redirect limits'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'name: Crawler behavior report' \
	'crawler behavior issue form is identified'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'label: Affected origin' \
	'crawler behavior issue form asks for the affected origin'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'label: Approximate request time' \
	'crawler behavior issue form asks for the request time'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'label: Requested path' \
	'crawler behavior issue form asks for the requested path'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'label: Observed JoshBot User-Agent' \
	'crawler behavior issue form asks for the observed user agent'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'label: Relevant robots.txt' \
	'crawler behavior issue form asks for relevant robots rules'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'label: What behavior appeared wrong?' \
	'crawler behavior issue form asks what went wrong'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'Do not include credentials' \
	'crawler behavior issue form warns against publishing credentials'

require_text \
	.github/ISSUE_TEMPLATE/crawler_report.yml \
	'reports are welcome even when your robots.txt configuration appears correct' \
	'crawler behavior issue form welcomes reports when robots appears correct'

require_text \
	.github/ISSUE_TEMPLATE/feature_request.yml \
	'https://github.com/joshternet/spec' \
	'feature request directs protocol changes to the specification repository'

require_text \
	docs/architecture.md \
	'internal/netguard' \
	'architecture documentation covers network safety'

require_text \
	docs/architecture.md \
	'internal/robots' \
	'architecture documentation covers robots enforcement'

require_text \
	docs/architecture.md \
	'internal/declaration' \
	'architecture documentation covers declaration verification'

require_text \
	docs/architecture.md \
	'internal/store' \
	'architecture documentation covers PostgreSQL state'

require_text \
	docs/architecture.md \
	'internal/discovery' \
	'architecture documentation covers discovery'

require_text \
	docs/architecture.md \
	'internal/publicdata' \
	'architecture documentation covers registry snapshots'

require_text \
	docs/architecture.md \
	'internal/githubpublish' \
	'architecture documentation covers publication'

require_text \
	SECURITY.md \
	'https://github.com/joshternet/joshbot/security/advisories/new' \
	'security policy uses GitHub private vulnerability reporting'

reject_text \
	SECURITY.md \
	'mailto:' \
	'security policy does not require email'

require_text \
	CODE_OF_CONDUCT.md \
	'https://github.com/joshternet/joshbot/issues/new?template=conduct_report.yml' \
	'Code of Conduct links to the conduct issue form'

require_text \
	CODE_OF_CONDUCT.md \
	'public' \
	'Code of Conduct warns that conduct issues are public'

reject_text \
	CODE_OF_CONDUCT.md \
	'mailto:' \
	'Code of Conduct does not require email'

require_text \
	CHANGELOG.md \
	'## [1.0.0] - 2026-09-05' \
	'changelog contains the initial release'

require_text \
	deploy/.env.example \
	'JOSHBOT_IMAGE=joshbot:v1.0.0' \
	'example environment uses the release image label'

require_text \
	compose.yaml \
	'image: ${JOSHBOT_IMAGE:-joshbot:v1.0.0}' \
	'Compose uses the release image label'

require_text \
	deploy/README.md \
	'docker compose' \
	'deployment guide documents Docker Compose'

require_text \
	deploy/README.md \
	'./deploy/publish.sh' \
	'deployment guide documents publication'

require_text \
	deploy/README.md \
	'./deploy/publish_test.sh' \
	'deployment guide documents publication validation'

require_text \
	deploy/README.md \
	'./deploy/smoke.sh' \
	'deployment guide documents recovery validation'

numbered_stage_pattern='[Pp][Hh][Aa][Ss][Ee][[:space:]_-]*[0-9]'

numbered_stage_matches=$(
	scan_release_files "$numbered_stage_pattern"
)

if [ -n "$numbered_stage_matches" ]; then
	fail "numbered development-stage language remains in the current tree"
	printf '%s\n' "$numbered_stage_matches" >&2
else
	pass "current tree contains no numbered development-stage language"
fi

unfinished_pattern='(^|[^[:alnum:]_])(TODO|FIXME)([^[:alnum:]_]|$)'

unfinished_matches=$(
	scan_release_files "$unfinished_pattern"
)

if [ -n "$unfinished_matches" ]; then
	fail "unfinished TODO or FIXME markers remain in the current tree"
	printf '%s\n' "$unfinished_matches" >&2
else
	pass "current tree contains no unfinished TODO or FIXME markers"
fi

tracked_sensitive_files=$(
	git ls-files |
	grep -E \
		'(^|/)deploy/\.env$|(^|/)\.env$|(^|/)secrets?/|\.dump$|\.partial$|(^|/)Docker\.raw$' ||
	true
)

if [ -n "$tracked_sensitive_files" ]; then
	fail "sensitive or runtime-only files are tracked"
	printf '%s\n' "$tracked_sensitive_files" >&2
else
	pass "no sensitive or runtime-only files are tracked"
fi

if git ls-files --error-unmatch deploy/.env >/dev/null 2>&1; then
	fail "deploy/.env is tracked"
else
	pass "deploy/.env is not tracked"
fi

if git ls-files |
	grep -E '(^|/)(coverage\.out|coverage\.html|test-results\.json)$' \
		>/dev/null 2>&1; then
	fail "generated quality artifacts are tracked"
else
	pass "generated quality artifacts are not tracked"
fi

if [ "$failures" -ne 0 ]; then
	printf '\nFAIL: release readiness found %d problem(s)\n' \
		"$failures" >&2
	exit 1
fi

printf '\nPASS: release readiness checks passed\n'