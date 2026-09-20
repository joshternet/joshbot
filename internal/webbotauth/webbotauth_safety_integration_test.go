package webbotauth_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

func TestWebBotAuthSafetyIntegrationExposesOnlyPublicIdentity(
	t *testing.T,
) {
	identity, privateKey :=
		newWebBotAuthSafetyIntegrationIdentity(t)

	jwk := identity.PublicJWK()

	if jwk.Kty != "OKP" {
		t.Errorf(
			"JWK kty = %q, want OKP",
			jwk.Kty,
		)
	}

	if jwk.Crv != "Ed25519" {
		t.Errorf(
			"JWK crv = %q, want Ed25519",
			jwk.Crv,
		)
	}

	if jwk.X == "" {
		t.Error(
			"JWK x is empty",
		)
	}

	if len(identity.KeyID()) != 43 {
		t.Errorf(
			"KeyID length = %d, want 43",
			len(identity.KeyID()),
		)
	}

	publicKey := identity.PublicKey()

	if len(publicKey) !=
		ed25519.PublicKeySize {
		t.Errorf(
			"PublicKey length = %d, want %d",
			len(publicKey),
			ed25519.PublicKeySize,
		)
	}

	encodedJWK, err := json.Marshal(jwk)
	if err != nil {
		t.Fatalf(
			"json.Marshal(PublicJWK) error = %v",
			err,
		)
	}

	if strings.Contains(
		string(encodedJWK),
		`"d"`,
	) {
		t.Errorf(
			"public JWK unexpectedly contains private d member: %s",
			encodedJWK,
		)
	}

	identityFormatted :=
		fmt.Sprintf("%v", identity)

	if !strings.Contains(
		identityFormatted,
		identity.KeyID(),
	) {
		t.Errorf(
			"formatted identity = %q, want key ID",
			identityFormatted,
		)
	}

	if !strings.Contains(
		identityFormatted,
		"private_key:<redacted>",
	) {
		t.Errorf(
			"formatted identity = %q, want private-key redaction",
			identityFormatted,
		)
	}

	privateEncoding :=
		fmt.Sprintf("%x", []byte(privateKey))

	if strings.Contains(
		identityFormatted,
		privateEncoding,
	) {
		t.Fatal(
			"formatted identity exposed private key",
		)
	}

	identityLog := identity.LogValue()

	if identityLog.Kind() != slog.KindGroup {
		t.Fatalf(
			"identity LogValue kind = %v, want group",
			identityLog.Kind(),
		)
	}

	identityAttributes :=
		identityLog.Group()

	if len(identityAttributes) != 1 ||
		identityAttributes[0].Key != "key_id" ||
		identityAttributes[0].Value.String() !=
			identity.KeyID() {
		t.Errorf(
			"identity LogValue = %#v",
			identityAttributes,
		)
	}

	signer, err := identity.Signer()
	if err != nil {
		t.Fatalf(
			"Identity.Signer() error = %v",
			err,
		)
	}

	signerFormatted :=
		fmt.Sprintf("%v", signer)

	if !strings.Contains(
		signerFormatted,
		identity.KeyID(),
	) ||
		!strings.Contains(
			signerFormatted,
			"private_key:<redacted>",
		) {
		t.Errorf(
			"formatted signer = %q",
			signerFormatted,
		)
	}

	if strings.Contains(
		signerFormatted,
		privateEncoding,
	) {
		t.Fatal(
			"formatted signer exposed private key",
		)
	}

	signerLog := signer.LogValue()

	if signerLog.Kind() != slog.KindGroup {
		t.Fatalf(
			"signer LogValue kind = %v, want group",
			signerLog.Kind(),
		)
	}

	signerAttributes :=
		signerLog.Group()

	if len(signerAttributes) != 1 ||
		signerAttributes[0].Key != "key_id" ||
		signerAttributes[0].Value.String() !=
			identity.KeyID() {
		t.Errorf(
			"signer LogValue = %#v",
			signerAttributes,
		)
	}
}

func TestWebBotAuthSafetyIntegrationHandlesNilReceivers(
	t *testing.T,
) {
	var identity *webbotauth.Identity

	if identity.PublicKey() != nil {
		t.Error(
			"nil Identity.PublicKey() != nil",
		)
	}

	if identity.PublicJWK() !=
		(webbotauth.PublicJWK{}) {
		t.Errorf(
			"nil Identity.PublicJWK() = %#v",
			identity.PublicJWK(),
		)
	}

	if identity.KeyID() != "" {
		t.Errorf(
			"nil Identity.KeyID() = %q, want empty",
			identity.KeyID(),
		)
	}

	if _, err := identity.Signer(); !errors.Is(
		err,
		webbotauth.ErrIdentityUnavailable,
	) {
		t.Errorf(
			"nil Identity.Signer() error = %v, want %v",
			err,
			webbotauth.ErrIdentityUnavailable,
		)
	}

	if got := fmt.Sprintf(
		"%v",
		identity,
	); got != "<nil>" {
		t.Errorf(
			"formatted nil identity = %q, want <nil>",
			got,
		)
	}

	if got := identity.LogValue().String(); got !=
		"<nil>" {
		t.Errorf(
			"nil identity LogValue = %q, want <nil>",
			got,
		)
	}

	var signer *webbotauth.Signer

	if err := signer.Sign(
		&http.Request{},
	); !errors.Is(
		err,
		webbotauth.ErrSignerUnavailable,
	) {
		t.Errorf(
			"nil Signer.Sign() error = %v, want %v",
			err,
			webbotauth.ErrSignerUnavailable,
		)
	}

	if got := fmt.Sprintf(
		"%v",
		signer,
	); got != "<nil>" {
		t.Errorf(
			"formatted nil signer = %q, want <nil>",
			got,
		)
	}

	if got := signer.LogValue().String(); got !=
		"<nil>" {
		t.Errorf(
			"nil signer LogValue = %q, want <nil>",
			got,
		)
	}
}

func TestWebBotAuthSafetyIntegrationRejectsInvalidSigningInputs(
	t *testing.T,
) {
	identity, privateKey :=
		newWebBotAuthSafetyIntegrationIdentity(t)

	if _, err := webbotauth.NewSigner(
		ed25519.PrivateKey{},
		identity.KeyID(),
	); !errors.Is(
		err,
		webbotauth.ErrInvalidSigningKey,
	) {
		t.Errorf(
			"short signing key error = %v, want %v",
			err,
			webbotauth.ErrInvalidSigningKey,
		)
	}

	if _, err := webbotauth.NewSigner(
		privateKey,
		"not-a-thumbprint",
	); !errors.Is(
		err,
		webbotauth.ErrInvalidKeyID,
	) {
		t.Errorf(
			"invalid key ID error = %v, want %v",
			err,
			webbotauth.ErrInvalidKeyID,
		)
	}

	signer, err := identity.Signer()
	if err != nil {
		t.Fatalf(
			"Identity.Signer() error = %v",
			err,
		)
	}

	if err := signer.Sign(nil); !errors.Is(
		err,
		webbotauth.ErrInvalidRequest,
	) {
		t.Errorf(
			"Signer.Sign(nil) error = %v, want %v",
			err,
			webbotauth.ErrInvalidRequest,
		)
	}

	if err := signer.Sign(
		&http.Request{},
	); !errors.Is(
		err,
		webbotauth.ErrInvalidRequest,
	) {
		t.Errorf(
			"Signer.Sign(empty request) error = %v, want %v",
			err,
			webbotauth.ErrInvalidRequest,
		)
	}

	request, err := http.NewRequest(
		http.MethodGet,
		"ftp://example.com/resource",
		nil,
	)
	if err != nil {
		t.Fatalf(
			"http.NewRequest() error = %v",
			err,
		)
	}

	if err := signer.Sign(request); !errors.Is(
		err,
		webbotauth.ErrInvalidRequest,
	) {
		t.Errorf(
			"Signer.Sign(ftp) error = %v, want %v",
			err,
			webbotauth.ErrInvalidRequest,
		)
	}
}

func TestWebBotAuthSafetyIntegrationBoundsPrivateKeyInput(
	t *testing.T,
) {
	if _, err := webbotauth.ReadIdentity(
		nil,
	); !errors.Is(
		err,
		webbotauth.ErrReadPrivateKey,
	) {
		t.Errorf(
			"ReadIdentity(nil) error = %v, want %v",
			err,
			webbotauth.ErrReadPrivateKey,
		)
	}

	oversized := bytes.Repeat(
		[]byte("x"),
		4097,
	)

	if _, err := webbotauth.ReadIdentity(
		bytes.NewReader(oversized),
	); !errors.Is(
		err,
		webbotauth.ErrPrivateKeyTooLarge,
	) {
		t.Errorf(
			"oversized ReadIdentity() error = %v, want %v",
			err,
			webbotauth.ErrPrivateKeyTooLarge,
		)
	}

	if _, err := webbotauth.ParsePrivateKeyPEM(
		[]byte("not a PEM key"),
	); !errors.Is(
		err,
		webbotauth.ErrInvalidPrivateKey,
	) {
		t.Errorf(
			"invalid PEM error = %v, want %v",
			err,
			webbotauth.ErrInvalidPrivateKey,
		)
	}
}

func newWebBotAuthSafetyIntegrationIdentity(
	t *testing.T,
) (*webbotauth.Identity, ed25519.PrivateKey) {
	t.Helper()

	seed := make(
		[]byte,
		ed25519.SeedSize,
	)

	for index := range seed {
		seed[index] = byte(index + 17)
	}

	privateKey :=
		ed25519.NewKeyFromSeed(seed)

	der, err :=
		x509.MarshalPKCS8PrivateKey(
			privateKey,
		)
	if err != nil {
		t.Fatalf(
			"MarshalPKCS8PrivateKey() error = %v",
			err,
		)
	}

	data := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		},
	)

	if data == nil {
		t.Fatal(
			"pem.EncodeToMemory() returned nil",
		)
	}

	identity, err :=
		webbotauth.ReadIdentity(
			bytes.NewReader(data),
		)
	if err != nil {
		t.Fatalf(
			"ReadIdentity() error = %v",
			err,
		)
	}

	return identity, privateKey
}
