package webbotauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestReadIdentityDerivesPublicJWKAndThumbprint(
	t *testing.T,
) {
	publicKey, privateKey, err :=
		ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pemData := testPrivateKeyPEM(
		t,
		privateKey,
	)

	identity, err := ReadIdentity(
		bytes.NewReader(pemData),
	)
	if err != nil {
		t.Fatalf(
			"ReadIdentity() error = %v, want nil",
			err,
		)
	}

	wantX := base64.RawURLEncoding.EncodeToString(
		publicKey,
	)

	wantJWK := PublicJWK{
		Crv: "Ed25519",
		Kty: "OKP",
		X:   wantX,
	}

	if got := identity.PublicJWK(); got != wantJWK {
		t.Errorf(
			"PublicJWK() = %#v, want %#v",
			got,
			wantJWK,
		)
	}

	if got := identity.PublicKey(); !bytes.Equal(got, publicKey) {
		t.Error(
			"PublicKey() does not match derived public key",
		)
	}

	canonical :=
		`{"crv":"Ed25519","kty":"OKP","x":"` +
			wantX +
			`"}`

	sum := sha256.Sum256(
		[]byte(canonical),
	)

	wantID :=
		base64.RawURLEncoding.EncodeToString(
			sum[:],
		)

	if got := identity.KeyID(); got != wantID {
		t.Errorf(
			"KeyID() = %q, want %q",
			got,
			wantID,
		)
	}

	signer, err := identity.Signer()
	if err != nil {
		t.Fatalf(
			"Signer() error = %v, want nil",
			err,
		)
	}

	if signer == nil {
		t.Fatal(
			"Signer() = nil, want signer",
		)
	}

	first := identity.PublicKey()
	first[0] ^= 0xff

	if bytes.Equal(
		first,
		identity.PublicKey(),
	) {
		t.Error(
			"PublicKey() returned shared mutable storage",
		)
	}
}

func TestJWKThumbprintMatchesRFC8037(
	t *testing.T,
) {
	jwk := PublicJWK{
		Crv: "Ed25519",
		Kty: "OKP",
		X:   "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo",
	}

	const want = "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"

	if got := jwkThumbprint(jwk); got != want {
		t.Errorf(
			"thumbprint = %q, want %q",
			got,
			want,
		)
	}
}

func TestPublicJWKNeverContainsPrivateMaterial(
	t *testing.T,
) {
	_, privateKey, err :=
		ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := NewIdentity(
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(
		identity.PublicJWK(),
	)
	if err != nil {
		t.Fatal(err)
	}

	var object map[string]any
	if err := json.Unmarshal(
		data,
		&object,
	); err != nil {
		t.Fatal(err)
	}

	if _, found := object["d"]; found {
		t.Fatal(
			"public JWK contains private d member",
		)
	}

	if len(object) != 3 ||
		object["kty"] != "OKP" ||
		object["crv"] != "Ed25519" ||
		object["x"] == "" {
		t.Fatalf(
			"public JWK = %s, want only crv, kty, x",
			data,
		)
	}
}

func TestIdentityFormattingAndErrorsDoNotLeakPrivateMaterial(
	t *testing.T,
) {
	_, privateKey, err :=
		ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := NewIdentity(
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}

	secretForms := []string{
		base64.RawURLEncoding.EncodeToString(
			privateKey[:ed25519.SeedSize],
		),
		base64.StdEncoding.EncodeToString(
			privateKey,
		),
		fmt.Sprintf(
			"%x",
			privateKey,
		),
	}

	rendered := fmt.Sprintf(
		"%v %+v %#v %s %q",
		identity,
		identity,
		identity,
		identity,
		identity,
	)

	var logBuffer bytes.Buffer
	logger := slog.New(
		slog.NewTextHandler(
			&logBuffer,
			nil,
		),
	)

	logger.Info(
		"identity",
		"signing_identity",
		identity,
	)

	rendered += logBuffer.String()

	for _, secret := range secretForms {
		if strings.Contains(
			rendered,
			secret,
		) {
			t.Fatal(
				"formatted or logged identity leaked private material",
			)
		}
	}

	marshaled, err := json.Marshal(
		identity,
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, secret := range secretForms {
		if strings.Contains(
			string(marshaled),
			secret,
		) {
			t.Fatal(
				"JSON identity dump leaked private material",
			)
		}
	}

	marker :=
		"PRIVATE-KEY-MATERIAL-MUST-NOT-LEAK"

	block := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: []byte(marker),
		},
	)

	_, err = ParsePrivateKeyPEM(block)
	if err == nil {
		t.Fatal(
			"ParsePrivateKeyPEM() error = nil, want error",
		)
	}

	if strings.Contains(
		err.Error(),
		marker,
	) {
		t.Fatalf(
			"error leaked private material: %v",
			err,
		)
	}
}

func TestNewIdentityRejectsInvalidAndMismatchedKeys(
	t *testing.T,
) {
	identity, err := NewIdentity(
		make(
			ed25519.PrivateKey,
			ed25519.PrivateKeySize-1,
		),
	)

	if identity != nil ||
		!errors.Is(
			err,
			ErrInvalidPrivateKey,
		) {
		t.Fatalf(
			"short private key = %#v, %v",
			identity,
			err,
		)
	}

	_, privateKey, err :=
		ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	mismatched := append(
		ed25519.PrivateKey(nil),
		privateKey...,
	)
	mismatched[len(mismatched)-1] ^= 0xff

	identity, err = NewIdentity(
		mismatched,
	)

	if identity != nil ||
		!errors.Is(
			err,
			ErrPrivateKeyMismatch,
		) {
		t.Fatalf(
			"mismatched private key = %#v, %v",
			identity,
			err,
		)
	}
}

func TestReadIdentityRejectsInvalidInput(
	t *testing.T,
) {
	validPEM := func() []byte {
		_, key, err :=
			ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}

		return testPrivateKeyPEM(
			t,
			key,
		)
	}()

	ecKey, err := ecdsa.GenerateKey(
		elliptic.P256(),
		rand.Reader,
	)
	if err != nil {
		t.Fatal(err)
	}

	ecDER, err :=
		x509.MarshalPKCS8PrivateKey(
			ecKey,
		)
	if err != nil {
		t.Fatal(err)
	}

	ecPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: ecDER,
		},
	)

	withHeader := pem.EncodeToMemory(
		&pem.Block{
			Type: "PRIVATE KEY",
			Headers: map[string]string{
				"Comment": "unsupported",
			},
			Bytes: []byte("x"),
		},
	)

	tests := []struct {
		name   string
		reader io.Reader
		want   error
	}{
		{
			name: "nil reader",
			want: ErrReadPrivateKey,
		},
		{
			name:   "read failure",
			reader: failingIdentityReader{},
			want:   ErrReadPrivateKey,
		},
		{
			name: "too large",
			reader: bytes.NewReader(
				bytes.Repeat(
					[]byte{'x'},
					maxPrivateKeyPEMSize+1,
				),
			),
			want: ErrPrivateKeyTooLarge,
		},
		{
			name:   "empty",
			reader: bytes.NewReader(nil),
			want:   ErrInvalidPrivateKey,
		},
		{
			name: "not PEM",
			reader: strings.NewReader(
				"not pem",
			),
			want: ErrInvalidPrivateKey,
		},
		{
			name: "wrong PEM type",
			reader: strings.NewReader(
				"-----BEGIN PUBLIC KEY-----\n" +
					"AA==\n" +
					"-----END PUBLIC KEY-----\n",
			),
			want: ErrInvalidPrivateKey,
		},
		{
			name:   "PEM headers",
			reader: bytes.NewReader(withHeader),
			want:   ErrInvalidPrivateKey,
		},
		{
			name: "trailing data",
			reader: bytes.NewReader(
				append(
					append(
						[]byte(nil),
						validPEM...,
					),
					[]byte("not whitespace")...,
				),
			),
			want: ErrInvalidPrivateKey,
		},
		{
			name:   "wrong key type",
			reader: bytes.NewReader(ecPEM),
			want:   ErrUnsupportedPrivateKey,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				identity, err :=
					ReadIdentity(
						test.reader,
					)

				if identity != nil ||
					!errors.Is(
						err,
						test.want,
					) {
					t.Errorf(
						"ReadIdentity() = %#v, %v; want nil, %v",
						identity,
						err,
						test.want,
					)
				}
			},
		)
	}
}

func TestNilIdentityAccessors(
	t *testing.T,
) {
	var identity *Identity

	if identity.PublicKey() != nil {
		t.Error(
			"nil Identity.PublicKey() is non-nil",
		)
	}

	if identity.PublicJWK() !=
		(PublicJWK{}) {
		t.Error(
			"nil Identity.PublicJWK() is non-zero",
		)
	}

	if identity.KeyID() != "" {
		t.Error(
			"nil Identity.KeyID() is non-empty",
		)
	}

	signer, err := identity.Signer()
	if signer != nil ||
		!errors.Is(
			err,
			ErrIdentityUnavailable,
		) {
		t.Errorf(
			"nil Identity.Signer() = %#v, %v",
			signer,
			err,
		)
	}

	if got := fmt.Sprintf(
		"%v",
		identity,
	); got != "<nil>" {
		t.Errorf(
			"nil formatted identity = %q, want <nil>",
			got,
		)
	}

	if got := identity.LogValue().String(); got != "<nil>" {
		t.Errorf(
			"nil log value = %q, want <nil>",
			got,
		)
	}
}

func TestParsePrivateKeyPEMAcceptsTrailingWhitespace(
	t *testing.T,
) {
	_, privateKey, err :=
		ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	data := append(
		testPrivateKeyPEM(
			t,
			privateKey,
		),
		[]byte("\n \t\r\n")...,
	)

	identity, err :=
		ParsePrivateKeyPEM(data)

	if err != nil ||
		identity == nil {
		t.Fatalf(
			"ParsePrivateKeyPEM() = %#v, %v; want identity, nil",
			identity,
			err,
		)
	}
}

func testPrivateKeyPEM(
	t *testing.T,
	key ed25519.PrivateKey,
) []byte {
	t.Helper()

	der, err :=
		x509.MarshalPKCS8PrivateKey(
			key,
		)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		},
	)
}

type failingIdentityReader struct{}

func (failingIdentityReader) Read(
	[]byte,
) (int, error) {
	return 0, errors.New(
		"reader failure",
	)
}
