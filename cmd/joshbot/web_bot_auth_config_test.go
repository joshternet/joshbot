package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

func TestLoadWebBotAuthIdentity(
	t *testing.T,
) {
	t.Run("nil environment getter", func(t *testing.T) {
		identity, err :=
			loadWebBotAuthIdentity(nil)

		if identity != nil {
			t.Errorf(
				"identity = %#v, want nil",
				identity,
			)
		}

		if !errors.Is(
			err,
			errInvalidWebBotAuthConfiguration,
		) {
			t.Errorf(
				"error = %v, want invalid configuration",
				err,
			)
		}
	})

	t.Run("unset is currently unsigned", func(t *testing.T) {
		identity, err :=
			loadWebBotAuthIdentity(
				mapEnvironment{}.get,
			)

		if err != nil {
			t.Fatalf(
				"error = %v, want nil",
				err,
			)
		}

		if identity != nil {
			t.Errorf(
				"identity = %#v, want nil",
				identity,
			)
		}
	})

	t.Run("valid PKCS8 Ed25519 key", func(t *testing.T) {
		keyFile :=
			writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthPrivateKeyFileEnvironment: keyFile,
		}

		identity, err :=
			loadWebBotAuthIdentity(
				environment.get,
			)

		if err != nil {
			t.Fatalf(
				"error = %v, want nil",
				err,
			)
		}

		if identity == nil {
			t.Fatal(
				"identity = nil, want identity",
			)
		}

		if identity.KeyID() == "" {
			t.Error(
				"identity KeyID() is empty",
			)
		}

		jwk := identity.PublicJWK()

		if jwk.Kty != "OKP" ||
			jwk.Crv != "Ed25519" ||
			jwk.X == "" {
			t.Errorf(
				"PublicJWK() = %#v",
				jwk,
			)
		}
	})

	t.Run("surrounding whitespace is rejected", func(t *testing.T) {
		keyFile :=
			writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthPrivateKeyFileEnvironment: " " + keyFile,
		}

		identity, err :=
			loadWebBotAuthIdentity(
				environment.get,
			)

		if identity != nil {
			t.Errorf(
				"identity = %#v, want nil",
				identity,
			)
		}

		if !errors.Is(
			err,
			errInvalidWebBotAuthConfiguration,
		) {
			t.Errorf(
				"error = %v, want invalid configuration",
				err,
			)
		}
	})

	t.Run("missing file is generic", func(t *testing.T) {
		path := filepath.Join(
			t.TempDir(),
			"missing-private-key.pem",
		)

		environment := mapEnvironment{
			webBotAuthPrivateKeyFileEnvironment: path,
		}

		identity, err :=
			loadWebBotAuthIdentity(
				environment.get,
			)

		if identity != nil {
			t.Errorf(
				"identity = %#v, want nil",
				identity,
			)
		}

		if !errors.Is(
			err,
			errOpenWebBotAuthPrivateKeyFile,
		) {
			t.Errorf(
				"error = %v, want open failure",
				err,
			)
		}

		if strings.Contains(
			err.Error(),
			path,
		) {
			t.Error(
				"open error exposed configured file path",
			)
		}
	})

	t.Run("malformed content is generic", func(t *testing.T) {
		const marker = "PRIVATE-KEY-CONTENT-MUST-NOT-LEAK"

		keyFile := filepath.Join(
			t.TempDir(),
			"malformed.pem",
		)

		if err := os.WriteFile(
			keyFile,
			[]byte(marker),
			0o600,
		); err != nil {
			t.Fatal(err)
		}

		environment := mapEnvironment{
			webBotAuthPrivateKeyFileEnvironment: keyFile,
		}

		identity, err :=
			loadWebBotAuthIdentity(
				environment.get,
			)

		if identity != nil {
			t.Errorf(
				"identity = %#v, want nil",
				identity,
			)
		}

		if !errors.Is(
			err,
			errInvalidWebBotAuthPrivateKey,
		) {
			t.Errorf(
				"error = %v, want invalid private key",
				err,
			)
		}

		if !errors.Is(
			err,
			webbotauth.ErrInvalidPrivateKey,
		) {
			t.Errorf(
				"error = %v, want underlying private-key validation error",
				err,
			)
		}

		if strings.Contains(
			err.Error(),
			marker,
		) {
			t.Error(
				"validation error exposed private-key content",
			)
		}
	})
}

func TestCrawlerStartupValidatesWebBotAuthBeforeDatabase(
	t *testing.T,
) {
	keyFile := filepath.Join(
		t.TempDir(),
		"invalid-private-key.pem",
	)

	if err := os.WriteFile(
		keyFile,
		[]byte("not a private key"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	t.Run("worker", func(t *testing.T) {
		operations :=
			newRuntimeOperations(io.Discard)

		environment := mapEnvironment{
			workerIDEnvironment:                 "worker-test",
			webBotAuthPrivateKeyFileEnvironment: keyFile,
		}

		operations.getenv =
			environment.get

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthPrivateKey,
		) {
			t.Fatalf(
				"worker() error = %v, want Web Bot Auth key validation failure",
				err,
			)
		}
	})

	t.Run("discovery", func(t *testing.T) {
		operations :=
			newRuntimeOperations(io.Discard)

		environment :=
			validCrawlRuntimeEnvironment()

		environment[webBotAuthPrivateKeyFileEnvironment] =
			keyFile

		operations.getenv =
			environment.get

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthPrivateKey,
		) {
			t.Fatalf(
				"discover() error = %v, want Web Bot Auth key validation failure",
				err,
			)
		}
	})
}

func TestValidateWebBotAuthIdentity(
	t *testing.T,
) {
	if err := validateWebBotAuthIdentity(
		mapEnvironment{}.get,
	); err != nil {
		t.Errorf(
			"unsigned validation error = %v, want nil",
			err,
		)
	}

	keyFile :=
		writeWebBotAuthPrivateKey(t)

	environment := mapEnvironment{
		webBotAuthPrivateKeyFileEnvironment: keyFile,
	}

	if err := validateWebBotAuthIdentity(
		environment.get,
	); err != nil {
		t.Errorf(
			"configured validation error = %v, want nil",
			err,
		)
	}
}

func writeWebBotAuthPrivateKey(
	t *testing.T,
) string {
	t.Helper()

	_, privateKey, err :=
		ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	der, err :=
		x509.MarshalPKCS8PrivateKey(
			privateKey,
		)
	if err != nil {
		t.Fatal(err)
	}

	data := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		},
	)

	path := filepath.Join(
		t.TempDir(),
		"web-bot-auth-private-key.pem",
	)

	if err := os.WriteFile(
		path,
		data,
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	return path
}
