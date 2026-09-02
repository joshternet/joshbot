package database

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDatabaseURL = "postgres://joshbot_app@localhost:5432/joshbot?sslmode=disable"

func TestLoadConfigReadsPasswordFile(t *testing.T) {
	tests := []struct {
		name     string
		fileData string
		password string
	}{
		{
			name:     "without trailing newline",
			fileData: "correct-horse-battery-staple",
			password: "correct-horse-battery-staple",
		},
		{
			name:     "with trailing newline",
			fileData: "correct-horse-battery-staple\n",
			password: "correct-horse-battery-staple",
		},
		{
			name: "maximum password with trailing newline",
			fileData: strings.Repeat(
				"x",
				maxDatabasePasswordSize,
			) + "\n",
			password: strings.Repeat(
				"x",
				maxDatabasePasswordSize,
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			passwordFile := writeDatabasePasswordFile(
				t,
				test.fileData,
			)

			config, err := LoadConfig(
				testDatabaseURL,
				passwordFile,
			)
			if err != nil {
				t.Fatalf(
					"LoadConfig() error = %v, want nil",
					err,
				)
			}

			if config.ConnConfig.Password != test.password {
				t.Errorf(
					"configured password = %q, want %q",
					config.ConnConfig.Password,
					test.password,
				)
			}

			connectionString := config.ConnString()
			if connectionString != testDatabaseURL {
				t.Errorf(
					"connection string = %q, want %q",
					connectionString,
					testDatabaseURL,
				)
			}

			if strings.Contains(
				connectionString,
				test.password,
			) {
				t.Error(
					"connection string contains database password",
				)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidSettings(t *testing.T) {
	validPasswordFile := writeDatabasePasswordFile(
		t,
		"valid-test-password\n",
	)
	emptyPasswordFile := writeDatabasePasswordFile(
		t,
		"",
	)
	newlinePasswordFile := writeDatabasePasswordFile(
		t,
		"\n",
	)

	oversizedSecret := strings.Repeat(
		"do-not-leak-",
		maxDatabasePasswordSize,
	)
	oversizedPasswordFile := writeDatabasePasswordFile(
		t,
		oversizedSecret,
	)

	tests := []struct {
		name         string
		databaseURL  string
		passwordFile string
		want         error
		forbidden    string
	}{
		{
			name:         "missing database URL",
			databaseURL:  "",
			passwordFile: validPasswordFile,
			want:         errDatabaseURLUnavailable,
		},
		{
			name:         "missing password file setting",
			databaseURL:  testDatabaseURL,
			passwordFile: "",
			want:         errDatabasePasswordFileUnavailable,
		},
		{
			name:         "malformed database URL",
			databaseURL:  "%",
			passwordFile: validPasswordFile,
			want:         errInvalidDatabaseURL,
		},
		{
			name:         "unsupported URL scheme",
			databaseURL:  "https://localhost/joshbot",
			passwordFile: validPasswordFile,
			want:         errInvalidDatabaseURL,
		},
		{
			name: "invalid PostgreSQL URL parameter",
			databaseURL: "postgres://localhost/joshbot" +
				"?connect_timeout=not-a-number",
			passwordFile: validPasswordFile,
			want:         errInvalidDatabaseURL,
		},
		{
			name: "password in URL authority",
			databaseURL: "postgres://joshbot_app:" +
				"embedded-secret@localhost/joshbot",
			passwordFile: validPasswordFile,
			want:         errDatabasePasswordInURL,
			forbidden:    "embedded-secret",
		},
		{
			name: "password in URL query",
			databaseURL: "postgres://joshbot_app@" +
				"localhost/joshbot?password=query-secret",
			passwordFile: validPasswordFile,
			want:         errDatabasePasswordInURL,
			forbidden:    "query-secret",
		},
		{
			name:         "unreadable password file",
			databaseURL:  testDatabaseURL,
			passwordFile: filepath.Join(t.TempDir(), "missing"),
			want:         errOpenDatabasePasswordFile,
		},
		{
			name:         "empty password file",
			databaseURL:  testDatabaseURL,
			passwordFile: emptyPasswordFile,
			want:         errEmptyDatabasePassword,
		},
		{
			name:         "newline-only password file",
			databaseURL:  testDatabaseURL,
			passwordFile: newlinePasswordFile,
			want:         errEmptyDatabasePassword,
		},
		{
			name:         "oversized password file",
			databaseURL:  testDatabaseURL,
			passwordFile: oversizedPasswordFile,
			want:         errDatabasePasswordTooLarge,
			forbidden:    "do-not-leak",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := LoadConfig(
				test.databaseURL,
				test.passwordFile,
			)
			if !errors.Is(err, test.want) {
				t.Errorf(
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

			if test.forbidden != "" &&
				strings.Contains(
					err.Error(),
					test.forbidden,
				) {
				t.Errorf(
					"LoadConfig() error exposes %q",
					test.forbidden,
				)
			}
		})
	}
}

func TestReadDatabasePasswordReturnsSafeReadFailure(
	t *testing.T,
) {
	const secretError = "do-not-leak-reader-error"

	password, err := readDatabasePassword(
		failingDatabasePasswordReader{
			err: errors.New(secretError),
		},
	)
	if !errors.Is(
		err,
		errReadDatabasePasswordFile,
	) {
		t.Errorf(
			"readDatabasePassword() error = %v, "+
				"want errReadDatabasePasswordFile",
			err,
		)
	}

	if password != "" {
		t.Errorf(
			"readDatabasePassword() password = %q, want empty",
			password,
		)
	}

	if strings.Contains(err.Error(), secretError) {
		t.Error(
			"readDatabasePassword() error exposes reader error",
		)
	}
}

type failingDatabasePasswordReader struct {
	err error
}

func (reader failingDatabasePasswordReader) Read(
	[]byte,
) (int, error) {
	return 0, reader.err
}

func writeDatabasePasswordFile(
	t *testing.T,
	contents string,
) string {
	t.Helper()

	path := filepath.Join(
		t.TempDir(),
		"database-password",
	)
	if err := os.WriteFile(
		path,
		[]byte(contents),
		0o600,
	); err != nil {
		t.Fatalf(
			"write database password file: %v",
			err,
		)
	}

	return path
}
