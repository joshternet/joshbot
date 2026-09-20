package webbotauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

var errWebBotAuthFailureIntegrationRead = errors.New(
	"integration Web Bot Auth read failure",
)

type webBotAuthFailureIntegrationReader struct{}

func (webBotAuthFailureIntegrationReader) Read([]byte) (int, error) {
	return 0, errWebBotAuthFailureIntegrationRead
}

func TestWebBotAuthFailureIntegrationIdentityValidation(t *testing.T) {
	identity, err := NewIdentity(
		make(ed25519.PrivateKey, ed25519.PrivateKeySize-1),
	)
	if identity != nil || !errors.Is(err, ErrInvalidPrivateKey) {
		t.Errorf(
			"NewIdentity(short) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrInvalidPrivateKey,
		)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() error = %v", err)
	}

	mismatched := append(ed25519.PrivateKey(nil), privateKey...)
	mismatched[len(mismatched)-1] ^= 0xff

	identity, err = NewIdentity(mismatched)
	if identity != nil || !errors.Is(err, ErrPrivateKeyMismatch) {
		t.Errorf(
			"NewIdentity(mismatched) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrPrivateKeyMismatch,
		)
	}
}

func TestWebBotAuthFailureIntegrationIdentityReading(t *testing.T) {
	if identity, err := ReadIdentity(nil); identity != nil ||
		!errors.Is(err, ErrReadPrivateKey) {
		t.Errorf(
			"ReadIdentity(nil) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrReadPrivateKey,
		)
	}

	if identity, err := ReadIdentity(
		webBotAuthFailureIntegrationReader{},
	); identity != nil || !errors.Is(err, ErrReadPrivateKey) {
		t.Errorf(
			"ReadIdentity(read failure) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrReadPrivateKey,
		)
	}

	oversized := bytes.NewReader(
		bytes.Repeat(
			[]byte{'x'},
			maxPrivateKeyPEMSize+1,
		),
	)
	if identity, err := ReadIdentity(oversized); identity != nil ||
		!errors.Is(err, ErrPrivateKeyTooLarge) {
		t.Errorf(
			"ReadIdentity(oversized) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrPrivateKeyTooLarge,
		)
	}
}

func TestWebBotAuthFailureIntegrationPKCS8Validation(t *testing.T) {
	invalidDER := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: []byte("not PKCS8 DER"),
		},
	)

	if identity, err := ParsePrivateKeyPEM(invalidDER); identity != nil ||
		!errors.Is(err, ErrInvalidPrivateKey) {
		t.Errorf(
			"ParsePrivateKeyPEM(invalid DER) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrInvalidPrivateKey,
		)
	}

	ecKey, err := ecdsa.GenerateKey(
		elliptic.P256(),
		rand.Reader,
	)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}

	ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey() error = %v", err)
	}

	ecPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: ecDER,
		},
	)

	if identity, err := ParsePrivateKeyPEM(ecPEM); identity != nil ||
		!errors.Is(err, ErrUnsupportedPrivateKey) {
		t.Errorf(
			"ParsePrivateKeyPEM(ECDSA) = %#v, %v, want nil, %v",
			identity,
			err,
			ErrUnsupportedPrivateKey,
		)
	}
}

func TestWebBotAuthFailureIntegrationSignerDependencies(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() error = %v", err)
	}

	identity, err := NewIdentity(privateKey)
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	if signer, err := newSigner(
		privateKey,
		identity.KeyID(),
		nil,
		time.Now,
	); signer != nil || !errors.Is(err, errNonceSourceUnavailable) {
		t.Errorf(
			"newSigner(nil nonce) = %#v, %v, want nil, %v",
			signer,
			err,
			errNonceSourceUnavailable,
		)
	}

	if signer, err := newSigner(
		privateKey,
		identity.KeyID(),
		bytes.NewReader(make([]byte, nonceSize)),
		nil,
	); signer != nil || !errors.Is(err, errClockUnavailable) {
		t.Errorf(
			"newSigner(nil clock) = %#v, %v, want nil, %v",
			signer,
			err,
			errClockUnavailable,
		)
	}
}

func TestWebBotAuthFailureIntegrationSigningFailuresAndNilHeader(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() error = %v", err)
	}

	identity, err := NewIdentity(privateKey)
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	failingSigner, err := newSigner(
		privateKey,
		identity.KeyID(),
		webBotAuthFailureIntegrationReader{},
		func() time.Time {
			return time.Unix(100, 0).UTC()
		},
	)
	if err != nil {
		t.Fatalf("newSigner(failing nonce) error = %v", err)
	}

	request, err := http.NewRequest(
		http.MethodGet,
		"https://example.com/page",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	if err := failingSigner.Sign(request); !errors.Is(err, ErrNonceGeneration) ||
		!errors.Is(err, errWebBotAuthFailureIntegrationRead) {
		t.Errorf(
			"Signer.Sign(nonce failure) error = %v, want nonce generation failure",
			err,
		)
	}

	signer, err := newSigner(
		privateKey,
		identity.KeyID(),
		bytes.NewReader(make([]byte, nonceSize)),
		func() time.Time {
			return time.Unix(100, 0).UTC()
		},
	)
	if err != nil {
		t.Fatalf("newSigner() error = %v", err)
	}

	request, err = http.NewRequest(
		http.MethodGet,
		"https://example.com/page",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header = nil

	if err := signer.Sign(request); err != nil {
		t.Fatalf("Signer.Sign(nil header) error = %v", err)
	}

	if request.Header == nil ||
		request.Header.Get("Signature-Agent") == "" ||
		request.Header.Get("Signature-Input") == "" ||
		request.Header.Get("Signature") == "" {
		t.Errorf(
			"Signer.Sign(nil header) headers = %#v",
			request.Header,
		)
	}
}

var _ io.Reader = webBotAuthFailureIntegrationReader{}
