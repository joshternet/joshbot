// Package database loads safe PostgreSQL runtime configuration.
package database

import (
	"errors"
	"io"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

const maxDatabasePasswordSize = 4096

var (
	errDatabaseURLUnavailable = errors.New(
		"database: URL is unavailable",
	)
	errDatabasePasswordFileUnavailable = errors.New(
		"database: password file is unavailable",
	)
	errInvalidDatabaseURL = errors.New(
		"database: URL is invalid",
	)
	errDatabasePasswordInURL = errors.New(
		"database: URL must not contain a password",
	)
	errOpenDatabasePasswordFile = errors.New(
		"database: cannot open password file",
	)
	errReadDatabasePasswordFile = errors.New(
		"database: cannot read password file",
	)
	errEmptyDatabasePassword = errors.New(
		"database: password file is empty",
	)
	errDatabasePasswordTooLarge = errors.New(
		"database: password file is too large",
	)
)

// LoadConfig parses a password-free PostgreSQL URL and loads its password from
// a separate file.
//
// The returned configuration stores the password only in its in-memory
// Password field. Its printable connection string remains the original
// password-free URL.
func LoadConfig(
	databaseURL string,
	passwordFile string,
) (*pgxpool.Config, error) {
	if databaseURL == "" {
		return nil, errDatabaseURLUnavailable
	}

	if passwordFile == "" {
		return nil, errDatabasePasswordFileUnavailable
	}

	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		return nil, errInvalidDatabaseURL
	}

	if parsedURL.Scheme != "postgres" &&
		parsedURL.Scheme != "postgresql" {
		return nil, errInvalidDatabaseURL
	}

	passwordInAuthority := false
	if parsedURL.User != nil {
		_, passwordInAuthority =
			parsedURL.User.Password()
	}

	_, passwordInQuery :=
		parsedURL.Query()["password"]
	if passwordInAuthority || passwordInQuery {
		return nil, errDatabasePasswordInURL
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errInvalidDatabaseURL
	}

	password, err := readDatabasePasswordFile(
		passwordFile,
	)
	if err != nil {
		return nil, err
	}

	config.ConnConfig.Password = password

	return config, nil
}

func readDatabasePasswordFile(
	path string,
) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errOpenDatabasePasswordFile
	}
	defer file.Close()

	return readDatabasePassword(file)
}

func readDatabasePassword(
	reader io.Reader,
) (string, error) {
	limited := io.LimitReader(
		reader,
		int64(maxDatabasePasswordSize)+2,
	)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", errReadDatabasePasswordFile
	}

	if len(data) > 0 &&
		data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}

	if len(data) == 0 {
		return "", errEmptyDatabasePassword
	}

	if len(data) > maxDatabasePasswordSize {
		return "", errDatabasePasswordTooLarge
	}

	return string(data), nil
}
