package joshbot

import (
	"os"
	"strings"
	"testing"
)

func TestDeploymentGuideMakesDatabaseSecretsReadableToContainerUsers(
	t *testing.T,
) {
	contents, err := os.ReadFile("deploy/README.md")
	if err != nil {
		t.Fatalf(
			"read deploy/README.md: %v",
			err,
		)
	}

	guide := string(contents)

	permissionCommand := `  chmod 0444 \
    /srv/joshbot/secrets/postgres_admin_password \
    /srv/joshbot/secrets/joshbot_migrator_password \
    /srv/joshbot/secrets/joshbot_app_password \
    /srv/joshbot/secrets/joshbot_backup_password`

	if !strings.Contains(guide, permissionCommand) {
		t.Fatalf(
			"deploy/README.md does not set all database secrets to mode 0444 in the generation command",
		)
	}

	normalizedGuide := strings.Join(strings.Fields(guide), " ")
	requiredExplanation := []string{
		"The containing `/srv/joshbot/secrets` directory remains `0700 root:root`.",
		"The four database password files are `0444 root:root`.",
		"The files must be readable by the non-root users inside the containers",
		"Docker selectively mounts only the individual secret files required by each service.",
	}

	for _, text := range requiredExplanation {
		if !strings.Contains(normalizedGuide, text) {
			t.Errorf(
				"deploy/README.md does not contain %q",
				text,
			)
		}
	}
}
