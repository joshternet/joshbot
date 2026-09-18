package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

const webBotAuthPrivateKeyFileEnvironment = "JOSHBOT_WEB_BOT_AUTH_PRIVATE_KEY_FILE"

var (
	errInvalidWebBotAuthConfiguration = errors.New(
		"Web Bot Auth configuration is invalid",
	)

	errOpenWebBotAuthPrivateKeyFile = errors.New(
		"cannot open Web Bot Auth private key file",
	)

	errInvalidWebBotAuthPrivateKey = errors.New(
		"Web Bot Auth private key is invalid",
	)
)

func loadWebBotAuthIdentity(
	getenv environmentGetter,
) (*webbotauth.Identity, error) {
	if getenv == nil {
		return nil,
			errInvalidWebBotAuthConfiguration
	}

	path := getenv(
		webBotAuthPrivateKeyFileEnvironment,
	)

	if path == "" {
		return nil, nil
	}

	if strings.TrimSpace(path) != path {
		return nil, fmt.Errorf(
			"%w: %s must not contain surrounding whitespace",
			errInvalidWebBotAuthConfiguration,
			webBotAuthPrivateKeyFileEnvironment,
		)
	}

	return readWebBotAuthIdentityFile(path)
}

func validateWebBotAuthIdentity(
	getenv environmentGetter,
) error {
	_, err := loadWebBotAuthIdentity(
		getenv,
	)

	return err
}

func readWebBotAuthIdentityFile(
	path string,
) (*webbotauth.Identity, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil,
			errOpenWebBotAuthPrivateKeyFile
	}
	defer file.Close()

	identity, err :=
		webbotauth.ReadIdentity(file)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %w",
			errInvalidWebBotAuthPrivateKey,
			err,
		)
	}

	return identity, nil
}
