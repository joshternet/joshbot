package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/robots"
)

const (
	documentationRepositoryURL = "https://github.com/joshternet/joshbot"
	documentationSpecURL       = "https://github.com/joshternet/spec"
	documentationRelease       = "1.0.0"
	documentationReleaseDate   = "2026-09-05"
)

var (
	documentationNumberedStage = regexp.MustCompile(
		`(?i)\bphase[[:space:]_-]*[0-9]+\b`,
	)
	documentationUnfinishedMarker = regexp.MustCompile(
		`(?i)\b(?:TODO|FIXME)\b`,
	)
)

func TestReleaseDocumentation(t *testing.T) {
	t.Run("required files", testRequiredReleaseFiles)
	t.Run("command help", testDocumentedCommands)
	t.Run("crawler identity", testDocumentedCrawlerIdentity)
	t.Run("architecture", testDocumentedArchitecture)
	t.Run("repository routes", testRepositoryRoutes)
	t.Run("release version", testDocumentedReleaseVersion)
	t.Run("current language", testCurrentReleaseLanguage)
	t.Run("license", testReleaseLicense)
}

func testRequiredReleaseFiles(t *testing.T) {
	t.Helper()

	required := []string{
		"LICENSE",
		"README.md",
		"CHANGELOG.md",
		"CONTRIBUTING.md",
		"CODE_OF_CONDUCT.md",
		"SECURITY.md",
		"docs/architecture.md",
		"docs/crawler.md",
		"deploy/README.md",
		"deploy/.env.example",
		".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml",
		".github/ISSUE_TEMPLATE/crawler_report.yml",
		".github/ISSUE_TEMPLATE/conduct_report.yml",
		".github/ISSUE_TEMPLATE/config.yml",
		".github/pull_request_template.md",
		"scripts/release-check.sh",
	}

	for _, path := range required {
		path := path

		t.Run(path, func(t *testing.T) {
			info, err := os.Stat(
				documentationRepositoryPath(path),
			)
			if err != nil {
				t.Fatalf(
					"os.Stat(%q) error = %v, want nil",
					path,
					err,
				)
			}

			if !info.Mode().IsRegular() {
				t.Fatalf(
					"%q mode = %v, want regular file",
					path,
					info.Mode(),
				)
			}

			if info.Size() == 0 {
				t.Fatalf(
					"%q size = 0, want non-empty file",
					path,
				)
			}
		})
	}
}

func testDocumentedCommands(t *testing.T) {
	t.Helper()

	readme := readDocumentationFile(t, "README.md")
	usageLines := documentationUsageLines(t, helpText)

	for _, command := range usageLines {
		requireDocumentationText(
			t,
			"README.md",
			readme,
			command,
		)
	}
}

func testDocumentedCrawlerIdentity(t *testing.T) {
	t.Helper()

	readme := readDocumentationFile(t, "README.md")
	crawler := readDocumentationFile(t, "docs/crawler.md")

	for _, document := range []struct {
		name    string
		content string
	}{
		{
			name:    "README.md",
			content: readme,
		},
		{
			name:    "docs/crawler.md",
			content: crawler,
		},
	} {
		requireDocumentationText(
			t,
			document.name,
			document.content,
			robots.UserAgent,
		)
		requireDocumentationText(
			t,
			document.name,
			document.content,
			robots.BotInformationURL,
		)
	}

	requiredCrawlerSettings := []string{
		"JOSHBOT_CRAWL_MAX_DEPTH",
		"JOSHBOT_CRAWL_MAX_PAGES",
		"JOSHBOT_CRAWL_MAX_PAGE_BYTES",
		"JOSHBOT_CRAWL_REQUEST_DELAY",
		"JOSHBOT_CRAWL_REDIRECT_LIMIT",
	}

	for _, setting := range requiredCrawlerSettings {
		requireDocumentationText(
			t,
			"docs/crawler.md",
			crawler,
			setting,
		)
	}
}

func testDocumentedArchitecture(t *testing.T) {
	t.Helper()

	architecture := readDocumentationFile(
		t,
		"docs/architecture.md",
	)

	requiredComponents := []string{
		"cmd/joshbot",
		"internal/database",
		"internal/origin",
		"internal/netguard",
		"internal/robots",
		"internal/declaration",
		"internal/store",
		"internal/worker",
		"internal/discovery",
		"internal/publicdata",
		"internal/githubpublish",
		"PostgreSQL",
	}

	for _, component := range requiredComponents {
		requireDocumentationText(
			t,
			"docs/architecture.md",
			architecture,
			component,
		)
	}
}

func testRepositoryRoutes(t *testing.T) {
	t.Helper()

	readme := readDocumentationFile(t, "README.md")
	contributing := readDocumentationFile(
		t,
		"CONTRIBUTING.md",
	)
	crawler := readDocumentationFile(
		t,
		"docs/crawler.md",
	)
	featureRequest := readDocumentationFile(
		t,
		".github/ISSUE_TEMPLATE/feature_request.yml",
	)
	crawlerReport := readDocumentationFile(
		t,
		".github/ISSUE_TEMPLATE/crawler_report.yml",
	)
	security := readDocumentationFile(t, "SECURITY.md")
	conduct := readDocumentationFile(
		t,
		"CODE_OF_CONDUCT.md",
	)

	privateVulnerabilityURL :=
		documentationRepositoryURL +
			"/security/advisories/new"
	conductIssueURL :=
		documentationRepositoryURL +
			"/issues/new?template=conduct_report.yml"
	crawlerIssueURL :=
		documentationRepositoryURL +
			"/issues/new?template=crawler_report.yml"

	requireDocumentationText(
		t,
		"SECURITY.md",
		security,
		privateVulnerabilityURL,
	)
	requireDocumentationText(
		t,
		"CODE_OF_CONDUCT.md",
		conduct,
		conductIssueURL,
	)

	for _, document := range []struct {
		name    string
		content string
	}{
		{
			name:    "README.md",
			content: readme,
		},
		{
			name:    "CONTRIBUTING.md",
			content: contributing,
		},
		{
			name:    "docs/crawler.md",
			content: crawler,
		},
	} {
		requireDocumentationText(
			t,
			document.name,
			document.content,
			crawlerIssueURL,
		)
	}

	for _, document := range []struct {
		name    string
		content string
	}{
		{
			name:    "README.md",
			content: readme,
		},
		{
			name:    "CONTRIBUTING.md",
			content: contributing,
		},
		{
			name:    "feature_request.yml",
			content: featureRequest,
		},
	} {
		requireDocumentationText(
			t,
			document.name,
			document.content,
			documentationSpecURL,
		)
	}

	for _, expected := range []string{
		"Affected origin",
		"Approximate request time",
		"Requested path",
		"Observed JoshBot User-Agent",
		"robots.txt",
		"What behavior appeared wrong?",
		robots.UserAgent,
	} {
		requireDocumentationText(
			t,
			"crawler_report.yml",
			crawlerReport,
			expected,
		)
	}

	requireDocumentationText(
		t,
		"CODE_OF_CONDUCT.md",
		strings.ToLower(conduct),
		"public",
	)

	for _, document := range []struct {
		name    string
		content string
	}{
		{
			name:    "SECURITY.md",
			content: security,
		},
		{
			name:    "CODE_OF_CONDUCT.md",
			content: conduct,
		},
	} {
		if strings.Contains(
			strings.ToLower(document.content),
			"mailto:",
		) {
			t.Fatalf(
				"%s contains an email reporting route",
				document.name,
			)
		}
	}
}

func testDocumentedReleaseVersion(t *testing.T) {
	t.Helper()

	changelog := readDocumentationFile(
		t,
		"CHANGELOG.md",
	)
	environment := readDocumentationFile(
		t,
		"deploy/.env.example",
	)
	compose := readDocumentationFile(t, "compose.yaml")

	requireDocumentationText(
		t,
		"CHANGELOG.md",
		changelog,
		"## ["+
			documentationRelease+
			"] - "+
			documentationReleaseDate,
	)
	requireDocumentationText(
		t,
		"deploy/.env.example",
		environment,
		"JOSHBOT_IMAGE=joshbot:v"+
			documentationRelease,
	)
	requireDocumentationText(
		t,
		"compose.yaml",
		compose,
		"image: ${JOSHBOT_IMAGE:-joshbot:v"+
			documentationRelease+"}",
	)
}

func testCurrentReleaseLanguage(t *testing.T) {
	t.Helper()

	paths := []string{
		"README.md",
		"CHANGELOG.md",
		"CONTRIBUTING.md",
		"CODE_OF_CONDUCT.md",
		"SECURITY.md",
		"docs/architecture.md",
		"docs/crawler.md",
		"deploy/README.md",
		"deploy/.env.example",
		"compose.yaml",
	}

	for _, path := range paths {
		path := path

		t.Run(path, func(t *testing.T) {
			content := readDocumentationFile(t, path)

			if match := documentationNumberedStage.FindString(
				content,
			); match != "" {
				t.Fatalf(
					"%s contains obsolete development-stage text %q",
					path,
					match,
				)
			}

			if match := documentationUnfinishedMarker.FindString(
				content,
			); match != "" {
				t.Fatalf(
					"%s contains unfinished marker %q",
					path,
					match,
				)
			}
		})
	}
}

func testReleaseLicense(t *testing.T) {
	t.Helper()

	license := readDocumentationFile(t, "LICENSE")
	readme := readDocumentationFile(t, "README.md")

	requiredLicenseText := []string{
		"BSD 3-Clause License",
		"Copyright (c) 2026 Joshua Morris",
		"THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS " +
			"AND CONTRIBUTORS \"AS IS\"",
	}

	for _, expected := range requiredLicenseText {
		requireDocumentationText(
			t,
			"LICENSE",
			license,
			expected,
		)
	}

	requireDocumentationText(
		t,
		"README.md",
		readme,
		"[BSD-3-Clause](LICENSE)",
	)
}

func documentationUsageLines(
	t *testing.T,
	help string,
) []string {
	t.Helper()

	lines := strings.Split(help, "\n")
	commands := make([]string, 0)
	inUsage := false

	for _, line := range lines {
		switch {
		case line == "Usage:":
			inUsage = true

		case inUsage && line == "":
			if len(commands) == 0 {
				t.Fatal(
					"helpText Usage section contains no commands",
				)
			}

			return commands

		case inUsage:
			command := strings.TrimSpace(line)
			if command != "" {
				commands = append(commands, command)
			}
		}
	}

	t.Fatal("helpText contains no complete Usage section")

	return nil
}

func readDocumentationFile(
	t *testing.T,
	path string,
) string {
	t.Helper()

	content, err := os.ReadFile(
		documentationRepositoryPath(path),
	)
	if err != nil {
		t.Fatalf(
			"os.ReadFile(%q) error = %v, want nil",
			path,
			err,
		)
	}

	return string(content)
}

func requireDocumentationText(
	t *testing.T,
	path string,
	content string,
	expected string,
) {
	t.Helper()

	if !strings.Contains(content, expected) {
		t.Errorf(
			"%s does not contain %q",
			path,
			expected,
		)
	}
}

func documentationRepositoryPath(path string) string {
	return filepath.Join(
		"..",
		"..",
		filepath.FromSlash(path),
	)
}
