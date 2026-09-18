package main

import (
	"os"
	"strings"
	"testing"
)

func TestControlComposeCredentialBoundaries(t *testing.T) {
	compose := readControlDeploymentFile(t, "compose.yaml")
	controlService := composeService(compose, "control")
	reportService := composeService(compose, "report")
	workerService := composeService(compose, "worker")
	discoveryService := composeService(compose, "discovery")
	publisherService := composeService(compose, "publisher")

	for _, required := range []string{
		"profiles:\n      - control",
		"command:\n      - control",
		"postgres://joshbot_operator@postgres",
		"JOSHBOT_OPERATOR_TOKEN_FILE: /run/secrets/joshbot_operator_token",
		"joshbot_operator_password",
		"read_only: true",
		"no-new-privileges:true",
		"- control",
	} {
		if !strings.Contains(controlService, required) {
			t.Errorf("control service missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"joshbot_report_token",
		"joshbot_app_password",
		"JOSHBOT_GITHUB_TOKEN_FILE",
		"egress",
	} {
		if strings.Contains(controlService, forbidden) {
			t.Errorf("control service contains %q", forbidden)
		}
	}
	if !strings.Contains(reportService, "postgres://joshbot_reporter@postgres") ||
		strings.Contains(reportService, "joshbot_operator") {
		t.Error("reporting credential boundary is not independent")
	}
	for name, service := range map[string]string{
		"worker": workerService, "discovery": discoveryService, "publisher": publisherService,
	} {
		if strings.Contains(service, "JOSHBOT_OPERATOR_TOKEN_FILE") ||
			strings.Contains(service, "joshbot_operator_token") {
			t.Errorf("%s received operator token", name)
		}
	}
}

func TestControlDatabaseRolesAreLeastPrivilege(t *testing.T) {
	script := readControlDeploymentFile(t, "deploy/postgres/init/010-joshbot-roles.sh")
	for _, required := range []string{
		"CREATE ROLE joshbot_reporter",
		"CREATE ROLE joshbot_operator",
		"TO joshbot_reporter;",
		"joshbot_operator_password",
		"joshbot_reporter_password",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("role initialization missing %q", required)
		}
	}
	migration := readControlDeploymentFile(t, "internal/store/migrations/0011_operator_audit_events.sql")
	for _, required := range []string{
		"REVOKE ALL ON TABLE operator_audit_events FROM joshbot_app",
		"REVOKE ALL ON TABLE operator_audit_events FROM joshbot_reporter",
		"GRANT INSERT ON TABLE operator_audit_events TO joshbot_operator",
		"BEFORE UPDATE OR DELETE ON operator_audit_events",
		"operator_blocked",
		"GRANT UPDATE (operator_blocked, crawl_blocked)",
	} {
		if !strings.Contains(migration, required) {
			t.Errorf("operator migration missing %q", required)
		}
	}
}

func TestControlUsesSharedMinimalImageWithoutEmbeddedSecrets(t *testing.T) {
	dockerfile := readControlDeploymentFile(t, "Dockerfile")
	for _, required := range []string{
		"./cmd/joshbot",
		"FROM scratch",
		"USER 65532:65532",
		`ENTRYPOINT ["/joshbot"]`,
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"JOSHBOT_OPERATOR_TOKEN",
		"joshbot_operator_token",
		"JOSHBOT_REPORT_TOKEN",
	} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Dockerfile contains credential marker %q", forbidden)
		}
	}
}

func readControlDeploymentFile(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(documentationRepositoryPath(relative))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func composeService(compose, name string) string {
	lines := strings.Split(compose, "\n")
	started := false
	var selected []string
	for _, line := range lines {
		if line == "  "+name+":" {
			started = true
		} else if started && strings.HasPrefix(line, "  ") &&
			!strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			break
		}
		if started {
			selected = append(selected, line)
		}
	}
	return strings.Join(selected, "\n")
}
