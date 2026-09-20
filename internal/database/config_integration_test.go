package database

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const integrationDatabaseURL = "postgres://joshbot_app@localhost:5432/joshbot?sslmode=disable"

type integrationDatabaseFailingReader struct{}

func (integrationDatabaseFailingReader) Read(
	[]byte,
) (int, error) {
	return 0, errors.New(
		"integration read failure",
	)
}

func TestDatabaseConfigIntegrationLoadsPasswordFromFilesystem(
	t *testing.T,
) {
	passwordFile := filepath.Join(
		t.TempDir(),
		"database-password",
	)

	const password = "correct-horse-battery-staple"

	if err := os.WriteFile(
		passwordFile,
		[]byte(password+"\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write database password: %v",
			err,
		)
	}

	config, err := LoadConfig(
		integrationDatabaseURL,
		passwordFile,
	)
	if err != nil {
		t.Fatalf(
			"LoadConfig() error = %v",
			err,
		)
	}

	if config.ConnConfig.Password != password {
		t.Errorf(
			"configured password = %q, want %q",
			config.ConnConfig.Password,
			password,
		)
	}

	if got := config.ConnString(); got !=
		integrationDatabaseURL {
		t.Errorf(
			"ConnString() = %q, want %q",
			got,
			integrationDatabaseURL,
		)
	}

	if strings.Contains(
		config.ConnString(),
		password,
	) {
		t.Fatal(
			"printable connection string exposed password",
		)
	}
}

func TestDatabaseConfigIntegrationRejectsUnsafeAndInvalidConfiguration(
	t *testing.T,
) {
	validPasswordFile := filepath.Join(
		t.TempDir(),
		"valid-password",
	)

	if err := os.WriteFile(
		validPasswordFile,
		[]byte("integration-password\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write valid password: %v",
			err,
		)
	}

	emptyPasswordFile := filepath.Join(
		t.TempDir(),
		"empty-password",
	)

	if err := os.WriteFile(
		emptyPasswordFile,
		nil,
		0o600,
	); err != nil {
		t.Fatalf(
			"write empty password: %v",
			err,
		)
	}

	oversizedPasswordFile := filepath.Join(
		t.TempDir(),
		"oversized-password",
	)

	if err := os.WriteFile(
		oversizedPasswordFile,
		[]byte(
			strings.Repeat(
				"x",
				maxDatabasePasswordSize+1,
			),
		),
		0o600,
	); err != nil {
		t.Fatalf(
			"write oversized password: %v",
			err,
		)
	}

	tests := []struct {
		name         string
		databaseURL  string
		passwordFile string
		want         error
	}{
		{
			name:         "missing database URL",
			passwordFile: validPasswordFile,
			want:         errDatabaseURLUnavailable,
		},
		{
			name:        "missing password file",
			databaseURL: integrationDatabaseURL,
			want:        errDatabasePasswordFileUnavailable,
		},
		{
			name:         "malformed database URL",
			databaseURL:  "%",
			passwordFile: validPasswordFile,
			want:         errInvalidDatabaseURL,
		},
		{
			name:         "unsupported scheme",
			databaseURL:  "https://localhost/joshbot",
			passwordFile: validPasswordFile,
			want:         errInvalidDatabaseURL,
		},
		{
			name: "invalid PostgreSQL parameter",
			databaseURL: "postgres://localhost/joshbot" +
				"?connect_timeout=not-a-number",
			passwordFile: validPasswordFile,
			want:         errInvalidDatabaseURL,
		},
		{
			name: "password in authority",
			databaseURL: "postgres://joshbot:" +
				"embedded-secret@localhost/joshbot",
			passwordFile: validPasswordFile,
			want:         errDatabasePasswordInURL,
		},
		{
			name: "password in query",
			databaseURL: "postgres://joshbot@localhost/joshbot" +
				"?password=query-secret",
			passwordFile: validPasswordFile,
			want:         errDatabasePasswordInURL,
		},
		{
			name:        "missing password file on disk",
			databaseURL: integrationDatabaseURL,
			passwordFile: filepath.Join(
				t.TempDir(),
				"missing",
			),
			want: errOpenDatabasePasswordFile,
		},
		{
			name:         "empty password",
			databaseURL:  integrationDatabaseURL,
			passwordFile: emptyPasswordFile,
			want:         errEmptyDatabasePassword,
		},
		{
			name:         "oversized password",
			databaseURL:  integrationDatabaseURL,
			passwordFile: oversizedPasswordFile,
			want:         errDatabasePasswordTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				config, err := LoadConfig(
					test.databaseURL,
					test.passwordFile,
				)

				if !errors.Is(
					err,
					test.want,
				) {
					t.Fatalf(
						"LoadConfig() error = %v, want %v",
						err,
						test.want,
					)
				}

				if config != nil {
					t.Errorf(
						"LoadConfig() config = %#v, want nil",
						config,
					)
				}
			},
		)
	}
}

func TestDatabaseConfigIntegrationSanitizesReaderFailure(
	t *testing.T,
) {
	password, err := readDatabasePassword(
		integrationDatabaseFailingReader{},
	)

	if password != "" {
		t.Errorf(
			"password = %q, want empty",
			password,
		)
	}

	if !errors.Is(
		err,
		errReadDatabasePasswordFile,
	) {
		t.Fatalf(
			"readDatabasePassword() error = %v, want %v",
			err,
			errReadDatabasePasswordFile,
		)
	}

	if strings.Contains(
		err.Error(),
		"integration read failure",
	) {
		t.Fatal(
			"password reader implementation error leaked",
		)
	}
}
