package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

func TestLoadWebBotAuthConfig(t *testing.T) {
	t.Run("nil environment getter", func(t *testing.T) {
		config, err := loadWebBotAuthConfig(nil)

		if config.active != nil ||
			config.transition != nil {
			t.Errorf(
				"config = %#v, want empty",
				config,
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

	t.Run("required mode with valid active key succeeds", func(t *testing.T) {
		keyFile := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: keyFile,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}

		if config.mode != webBotAuthModeRequired {
			t.Errorf(
				"mode = %q, want %q",
				config.mode,
				webBotAuthModeRequired,
			)
		}

		if config.active == nil ||
			config.active.KeyID() == "" {
			t.Fatal("active identity is missing")
		}

		if config.transition != nil {
			t.Errorf(
				"transition = %#v, want nil",
				config.transition,
			)
		}
	})

	t.Run("default mode is required and missing active key fails", func(t *testing.T) {
		config, err := loadWebBotAuthConfig(
			mapEnvironment{}.get,
		)

		if config.active != nil {
			t.Errorf(
				"active = %#v, want nil",
				config.active,
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

	t.Run("required mode with unreadable active key fails", func(t *testing.T) {
		path := filepath.Join(
			t.TempDir(),
			"missing-private-key.pem",
		)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: path,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)

		if config.active != nil {
			t.Errorf(
				"active = %#v, want nil",
				config.active,
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

		if strings.Contains(err.Error(), path) {
			t.Error(
				"open error exposed configured file path",
			)
		}
	})

	t.Run("required mode with malformed active key fails", func(t *testing.T) {
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
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: keyFile,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)

		if config.active != nil {
			t.Errorf(
				"active = %#v, want nil",
				config.active,
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

		if strings.Contains(err.Error(), marker) {
			t.Error(
				"validation error exposed private-key content",
			)
		}
	})

	t.Run("explicit unsigned mode succeeds without a key", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment: webBotAuthModeUnsigned,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}

		if config.mode != webBotAuthModeUnsigned {
			t.Errorf(
				"mode = %q, want %q",
				config.mode,
				webBotAuthModeUnsigned,
			)
		}

		if config.active != nil ||
			config.transition != nil {
			t.Errorf(
				"config = %#v, want nil identities",
				config,
			)
		}

		signer, err := loadWebBotAuthSigner(
			environment.get,
		)
		if err != nil {
			t.Fatalf(
				"signer error = %v, want nil",
				err,
			)
		}

		if signer != nil {
			t.Errorf(
				"signer = %#v, want nil",
				signer,
			)
		}
	})

	t.Run("malformed mode fails", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment: "maybe",
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

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

	t.Run("active and legacy paths conflict", func(t *testing.T) {
		active := writeWebBotAuthPrivateKey(t)
		legacy := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: active,
			webBotAuthPrivateKeyFileEnvironment:       legacy,
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

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

	t.Run("legacy path loads as active", func(t *testing.T) {
		keyFile := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:           webBotAuthModeRequired,
			webBotAuthPrivateKeyFileEnvironment: keyFile,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}

		if config.active == nil {
			t.Fatal("active identity is missing")
		}
	})

	t.Run("optional transition identity loads successfully", func(t *testing.T) {
		active := writeWebBotAuthPrivateKey(t)
		transition := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                     webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment:     active,
			webBotAuthTransitionPrivateKeyFileEnvironment: transition,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}

		if config.active == nil ||
			config.transition == nil {
			t.Fatalf(
				"config = %#v, want active and transition",
				config,
			)
		}

		if config.active.KeyID() ==
			config.transition.KeyID() {
			t.Fatal(
				"active and transition KeyIDs unexpectedly match",
			)
		}
	})

	t.Run("duplicate active and transition identities fail", func(t *testing.T) {
		keyFile := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                     webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment:     keyFile,
			webBotAuthTransitionPrivateKeyFileEnvironment: keyFile,
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

		if !errors.Is(
			err,
			errDuplicateWebBotAuthIdentities,
		) {
			t.Errorf(
				"error = %v, want duplicate identities",
				err,
			)
		}
	})

	t.Run("malformed transition identity fails", func(t *testing.T) {
		active := writeWebBotAuthPrivateKey(t)
		transition := filepath.Join(
			t.TempDir(),
			"malformed-transition.pem",
		)

		if err := os.WriteFile(
			transition,
			[]byte("not-a-key"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                     webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment:     active,
			webBotAuthTransitionPrivateKeyFileEnvironment: transition,
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthPrivateKey,
		) {
			t.Errorf(
				"error = %v, want invalid private key",
				err,
			)
		}
	})

	t.Run("surrounding whitespace on active path fails", func(t *testing.T) {
		keyFile := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: " " + keyFile,
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

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

	t.Run("surrounding whitespace on transition path fails", func(t *testing.T) {
		active := writeWebBotAuthPrivateKey(t)
		transition := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                     webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment:     active,
			webBotAuthTransitionPrivateKeyFileEnvironment: " " + transition,
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

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

	t.Run("surrounding whitespace on legacy path fails", func(t *testing.T) {
		keyFile := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:           webBotAuthModeRequired,
			webBotAuthPrivateKeyFileEnvironment: " " + keyFile,
		}

		_, err := loadWebBotAuthConfig(
			environment.get,
		)

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
}

func TestActiveWebBotAuthKeySelection(t *testing.T) {
	identityA, pathA :=
		writeWebBotAuthPrivateKeyWithIdentity(t)
	identityB, pathB :=
		writeWebBotAuthPrivateKeyWithIdentity(t)

	signKeyID := func(
		t *testing.T,
		environment mapEnvironment,
	) string {
		t.Helper()

		signer, err := loadWebBotAuthSigner(
			environment.get,
		)
		if err != nil {
			t.Fatalf(
				"loadWebBotAuthSigner() error = %v",
				err,
			)
		}

		if signer == nil {
			t.Fatal("signer = nil, want signer")
		}

		request, err := http.NewRequest(
			http.MethodGet,
			"https://example.com/path",
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := signer.Sign(request); err != nil {
			t.Fatalf("Sign() error = %v", err)
		}

		input := request.Header.Get(
			"Signature-Input",
		)
		needle := `keyid="` + identityA.KeyID() + `"`
		if strings.Contains(input, needle) {
			return identityA.KeyID()
		}

		needle = `keyid="` + identityB.KeyID() + `"`
		if strings.Contains(input, needle) {
			return identityB.KeyID()
		}

		t.Fatalf(
			"Signature-Input = %q, want keyid for A or B",
			input,
		)

		return ""
	}

	t.Run("active A signs with A thumbprint", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: pathA,
		}

		if got := signKeyID(t, environment); got !=
			identityA.KeyID() {
			t.Fatalf(
				"keyid = %q, want %q",
				got,
				identityA.KeyID(),
			)
		}
	})

	t.Run("B active and A transition signs with B", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment:                     webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment:     pathB,
			webBotAuthTransitionPrivateKeyFileEnvironment: pathA,
		}

		config, err := loadWebBotAuthConfig(
			environment.get,
		)
		if err != nil {
			t.Fatalf(
				"loadWebBotAuthConfig() error = %v",
				err,
			)
		}

		if config.transition == nil ||
			config.transition.KeyID() !=
				identityA.KeyID() {
			t.Fatalf(
				"transition = %#v, want identity A",
				config.transition,
			)
		}

		got := signKeyID(t, environment)
		if got != identityB.KeyID() {
			t.Fatalf(
				"keyid = %q, want active B %q (transition must not sign)",
				got,
				identityB.KeyID(),
			)
		}
	})

	t.Run("removing transition leaves B signing", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: pathB,
		}

		if got := signKeyID(t, environment); got !=
			identityB.KeyID() {
			t.Fatalf(
				"keyid = %q, want %q",
				got,
				identityB.KeyID(),
			)
		}
	})
}

func TestCrawlerStartupValidatesWebBotAuthBeforeDatabase(
	t *testing.T,
) {
	t.Run("required missing active key fails worker", func(t *testing.T) {
		operations :=
			newRuntimeOperations(io.Discard)

		environment := mapEnvironment{
			workerIDEnvironment:       "worker-test",
			webBotAuthModeEnvironment: webBotAuthModeRequired,
		}

		operations.getenv = environment.get

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthConfiguration,
		) {
			t.Fatalf(
				"worker() error = %v, want missing Web Bot Auth configuration",
				err,
			)
		}
	})

	t.Run("required missing active key fails discovery", func(t *testing.T) {
		operations :=
			newRuntimeOperations(io.Discard)

		environment :=
			validCrawlRuntimeEnvironment()
		environment[webBotAuthModeEnvironment] =
			webBotAuthModeRequired

		operations.getenv = environment.get

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthConfiguration,
		) {
			t.Fatalf(
				"discover() error = %v, want missing Web Bot Auth configuration",
				err,
			)
		}
	})

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

	t.Run("worker rejects malformed required key", func(t *testing.T) {
		operations :=
			newRuntimeOperations(io.Discard)

		environment := mapEnvironment{
			workerIDEnvironment:                       "worker-test",
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: keyFile,
		}

		operations.getenv = environment.get

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

	t.Run("discovery rejects malformed required key", func(t *testing.T) {
		operations :=
			newRuntimeOperations(io.Discard)

		environment :=
			validCrawlRuntimeEnvironment()
		environment[webBotAuthModeEnvironment] =
			webBotAuthModeRequired
		environment[webBotAuthActivePrivateKeyFileEnvironment] =
			keyFile

		operations.getenv = environment.get

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

func TestLoadWebBotAuthIdentity(t *testing.T) {
	t.Run("required mode returns active identity", func(t *testing.T) {
		keyFile := writeWebBotAuthPrivateKey(t)

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: keyFile,
		}

		identity, err := loadWebBotAuthIdentity(
			environment.get,
		)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}

		if identity == nil || identity.KeyID() == "" {
			t.Fatal("identity is missing")
		}
	})

	t.Run("unsigned mode returns nil identity", func(t *testing.T) {
		identity, err := loadWebBotAuthIdentity(
			mapEnvironment{
				webBotAuthModeEnvironment: webBotAuthModeUnsigned,
			}.get,
		)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}

		if identity != nil {
			t.Errorf(
				"identity = %#v, want nil",
				identity,
			)
		}
	})

	t.Run("configuration errors propagate", func(t *testing.T) {
		identity, err := loadWebBotAuthIdentity(nil)

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
}

func TestValidateWebBotAuthIdentity(t *testing.T) {
	if err := validateWebBotAuthIdentity(
		mapEnvironment{
			webBotAuthModeEnvironment: webBotAuthModeUnsigned,
		}.get,
	); err != nil {
		t.Errorf(
			"unsigned validation error = %v, want nil",
			err,
		)
	}

	if err := validateWebBotAuthIdentity(
		mapEnvironment{}.get,
	); err == nil {
		t.Fatal(
			"default required validation error = nil, want failure",
		)
	}

	keyFile := writeWebBotAuthPrivateKey(t)

	environment := mapEnvironment{
		webBotAuthModeEnvironment:                 webBotAuthModeRequired,
		webBotAuthActivePrivateKeyFileEnvironment: keyFile,
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

func validateWebBotAuthIdentity(
	getenv environmentGetter,
) error {
	_, err := loadWebBotAuthSigner(getenv)

	return err
}

func writeWebBotAuthPrivateKey(
	t *testing.T,
) string {
	t.Helper()

	_, path := writeWebBotAuthPrivateKeyWithIdentity(t)

	return path
}

func writeWebBotAuthPrivateKeyWithIdentity(
	t *testing.T,
) (*webbotauth.Identity, string) {
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

	identity, err := webbotauth.ReadIdentity(
		strings.NewReader(string(data)),
	)
	if err != nil {
		t.Fatal(err)
	}

	return identity, path
}
