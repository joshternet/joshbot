package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

// Web Bot Auth configuration loads one active identity and at most one
// transition identity with an explicit required/unsigned mode so production
// crawler traffic cannot silently fall back to unsigned requests.
const (
	webBotAuthModeEnvironment = "JOSHBOT_WEB_BOT_AUTH_MODE"

	webBotAuthActivePrivateKeyFileEnvironment = "JOSHBOT_WEB_BOT_AUTH_ACTIVE_PRIVATE_KEY_FILE"

	webBotAuthTransitionPrivateKeyFileEnvironment = "JOSHBOT_WEB_BOT_AUTH_TRANSITION_PRIVATE_KEY_FILE"

	// Legacy alias for the active private-key path.
	webBotAuthPrivateKeyFileEnvironment = "JOSHBOT_WEB_BOT_AUTH_PRIVATE_KEY_FILE"

	webBotAuthModeRequired = "required"
	webBotAuthModeUnsigned = "unsigned"
)

var (
	errInvalidWebBotAuthConfiguration = errors.New(
		"web bot auth configuration is invalid",
	)

	errOpenWebBotAuthPrivateKeyFile = errors.New(
		"cannot open Web Bot Auth private key file",
	)

	errInvalidWebBotAuthPrivateKey = errors.New(
		"web bot auth private key is invalid",
	)

	errDuplicateWebBotAuthIdentities = errors.New(
		"web bot auth active and transition identities are the same",
	)
)

// webBotAuthConfig is the validated Web Bot Auth runtime configuration.
type webBotAuthConfig struct {
	mode       string
	active     *webbotauth.Identity
	transition *webbotauth.Identity
}

// loadWebBotAuthConfig loads and validates Web Bot Auth mode and identities.
//
// Mode defaults to required when unset. Only the active identity signs crawler
// requests. An optional transition identity is validated during rotation and
// must not share the active JWK thumbprint.
func loadWebBotAuthConfig(
	getenv environmentGetter,
) (webBotAuthConfig, error) {
	if getenv == nil {
		return webBotAuthConfig{},
			errInvalidWebBotAuthConfiguration
	}

	mode, err := parseWebBotAuthMode(
		getenv(webBotAuthModeEnvironment),
	)
	if err != nil {
		return webBotAuthConfig{}, err
	}

	if mode == webBotAuthModeUnsigned {
		return webBotAuthConfig{
			mode: webBotAuthModeUnsigned,
		}, nil
	}

	activePath, err :=
		resolveWebBotAuthActivePrivateKeyPath(
			getenv,
		)
	if err != nil {
		return webBotAuthConfig{}, err
	}

	if activePath == "" {
		return webBotAuthConfig{}, fmt.Errorf(
			"%w: %s is required when Web Bot Auth mode is %s",
			errInvalidWebBotAuthConfiguration,
			webBotAuthActivePrivateKeyFileEnvironment,
			webBotAuthModeRequired,
		)
	}

	active, err :=
		readWebBotAuthIdentityFile(activePath)
	if err != nil {
		return webBotAuthConfig{}, err
	}

	transitionPath, err :=
		configuredPrivateKeyPath(
			getenv,
			webBotAuthTransitionPrivateKeyFileEnvironment,
		)
	if err != nil {
		return webBotAuthConfig{}, err
	}

	var transition *webbotauth.Identity

	if transitionPath != "" {
		transition, err =
			readWebBotAuthIdentityFile(
				transitionPath,
			)
		if err != nil {
			return webBotAuthConfig{}, err
		}

		if active.KeyID() ==
			transition.KeyID() {
			return webBotAuthConfig{}, fmt.Errorf(
				"%w: %w",
				errInvalidWebBotAuthConfiguration,
				errDuplicateWebBotAuthIdentities,
			)
		}
	}

	return webBotAuthConfig{
		mode:       webBotAuthModeRequired,
		active:     active,
		transition: transition,
	}, nil
}

// loadWebBotAuthIdentity loads the active Web Bot Auth identity.
//
// In unsigned mode the identity is nil. In required mode the active identity
// must be present and valid.
func loadWebBotAuthIdentity(
	getenv environmentGetter,
) (*webbotauth.Identity, error) {
	config, err := loadWebBotAuthConfig(getenv)
	if err != nil {
		return nil, err
	}

	return config.active, nil
}

// loadWebBotAuthSigner constructs the active crawler request signer.
//
// Only the active identity signs requests. Transition identities are never
// used for signing.
func loadWebBotAuthSigner(
	getenv environmentGetter,
) (*webbotauth.Signer, error) {
	config, err := loadWebBotAuthConfig(getenv)
	if err != nil {
		return nil, err
	}

	if config.mode == webBotAuthModeUnsigned {
		return nil, nil
	}

	return config.active.Signer()
}

func parseWebBotAuthMode(
	value string,
) (string, error) {
	switch value {
	case "":
		return webBotAuthModeRequired, nil
	case webBotAuthModeRequired,
		webBotAuthModeUnsigned:
		return value, nil
	default:
		return "", fmt.Errorf(
			"%w: %s must be %s or %s",
			errInvalidWebBotAuthConfiguration,
			webBotAuthModeEnvironment,
			webBotAuthModeRequired,
			webBotAuthModeUnsigned,
		)
	}
}

func resolveWebBotAuthActivePrivateKeyPath(
	getenv environmentGetter,
) (string, error) {
	activePath, err := configuredPrivateKeyPath(
		getenv,
		webBotAuthActivePrivateKeyFileEnvironment,
	)
	if err != nil {
		return "", err
	}

	legacyPath, err := configuredPrivateKeyPath(
		getenv,
		webBotAuthPrivateKeyFileEnvironment,
	)
	if err != nil {
		return "", err
	}

	if activePath != "" && legacyPath != "" {
		return "", fmt.Errorf(
			"%w: set only one of %s or %s",
			errInvalidWebBotAuthConfiguration,
			webBotAuthActivePrivateKeyFileEnvironment,
			webBotAuthPrivateKeyFileEnvironment,
		)
	}

	if activePath != "" {
		return activePath, nil
	}

	return legacyPath, nil
}

func configuredPrivateKeyPath(
	getenv environmentGetter,
	name string,
) (string, error) {
	path := getenv(name)
	if path == "" {
		return "", nil
	}

	if strings.TrimSpace(path) != path {
		return "", fmt.Errorf(
			"%w: %s must not contain surrounding whitespace",
			errInvalidWebBotAuthConfiguration,
			name,
		)
	}

	return path, nil
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
