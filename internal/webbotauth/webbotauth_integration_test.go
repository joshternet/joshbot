package webbotauth_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

type webBotAuthIntegrationObservation struct {
	authority      string
	signatureAgent string
	signatureInput string
	signature      string
}

func TestWebBotAuthIntegrationLoadsIdentityAndSignsWireRequests(
	t *testing.T,
) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}

	privateKey := ed25519.NewKeyFromSeed(seed)

	der, err := x509.MarshalPKCS8PrivateKey(
		privateKey,
	)
	if err != nil {
		t.Fatalf(
			"MarshalPKCS8PrivateKey() error = %v",
			err,
		)
	}

	identity, err := webbotauth.ReadIdentity(
		bytes.NewReader(
			pem.EncodeToMemory(
				&pem.Block{
					Type:  "PRIVATE KEY",
					Bytes: der,
				},
			),
		),
	)
	if err != nil {
		t.Fatalf(
			"ReadIdentity() error = %v",
			err,
		)
	}

	signer, err := identity.Signer()
	if err != nil {
		t.Fatalf(
			"Identity.Signer() error = %v",
			err,
		)
	}

	var mu sync.Mutex

	observations := make(
		[]webBotAuthIntegrationObservation,
		0,
		2,
	)

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			mu.Lock()

			observations = append(
				observations,
				webBotAuthIntegrationObservation{
					authority: request.Host,
					signatureAgent: request.Header.Get(
						"Signature-Agent",
					),
					signatureInput: request.Header.Get(
						"Signature-Input",
					),
					signature: request.Header.Get(
						"Signature",
					),
				},
			)

			mu.Unlock()

			writer.WriteHeader(
				http.StatusNoContent,
			)
		}),
	)
	defer server.Close()

	client := server.Client()

	for attempt := 0; attempt < 2; attempt++ {
		request, err := http.NewRequest(
			http.MethodGet,
			server.URL+"/signed",
			nil,
		)
		if err != nil {
			t.Fatalf(
				"http.NewRequest() error = %v",
				err,
			)
		}

		request.Host = "EXAMPLE.com:80"

		if err := signer.Sign(request); err != nil {
			t.Fatalf(
				"Signer.Sign() error = %v",
				err,
			)
		}

		response, err := client.Do(request)
		if err != nil {
			t.Fatalf(
				"client.Do() error = %v",
				err,
			)
		}

		if err := response.Body.Close(); err != nil {
			t.Fatalf(
				"response.Body.Close() error = %v",
				err,
			)
		}

		if response.StatusCode !=
			http.StatusNoContent {
			t.Fatalf(
				"response status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
	}

	mu.Lock()

	got := append(
		[]webBotAuthIntegrationObservation(nil),
		observations...,
	)

	mu.Unlock()

	if len(got) != 2 {
		t.Fatalf(
			"observations = %d, want 2",
			len(got),
		)
	}

	for index, observation := range got {
		if observation.authority !=
			"EXAMPLE.com:80" {
			t.Errorf(
				"request %d wire Host = %q, want %q",
				index,
				observation.authority,
				"EXAMPLE.com:80",
			)
		}

		verifyWebBotAuthIntegrationSignature(
			t,
			identity,
			"example.com",
			observation,
		)
	}

	if got[0].signatureInput ==
		got[1].signatureInput {
		t.Error(
			"two signed requests reused Signature-Input",
		)
	}

	if got[0].signature ==
		got[1].signature {
		t.Error(
			"two signed requests reused Signature",
		)
	}
}

func verifyWebBotAuthIntegrationSignature(
	t *testing.T,
	identity *webbotauth.Identity,
	authority string,
	observation webBotAuthIntegrationObservation,
) {
	t.Helper()

	wantAgent :=
		`"` + webbotauth.SignatureAgentURL + `"`

	if observation.signatureAgent != wantAgent {
		t.Fatalf(
			"Signature-Agent = %q, want %q",
			observation.signatureAgent,
			wantAgent,
		)
	}

	const inputPrefix = "sig1="

	if !strings.HasPrefix(
		observation.signatureInput,
		inputPrefix,
	) {
		t.Fatalf(
			"Signature-Input = %q, want sig1 prefix",
			observation.signatureInput,
		)
	}

	parameters := strings.TrimPrefix(
		observation.signatureInput,
		inputPrefix,
	)

	if !strings.Contains(
		parameters,
		`keyid="`+identity.KeyID()+`"`,
	) {
		t.Errorf(
			"Signature-Input = %q, want identity key ID",
			observation.signatureInput,
		)
	}

	const signaturePrefix = "sig1=:"
	const signatureSuffix = ":"

	if !strings.HasPrefix(
		observation.signature,
		signaturePrefix,
	) ||
		!strings.HasSuffix(
			observation.signature,
			signatureSuffix,
		) {
		t.Fatalf(
			"Signature = %q, want sig1 byte sequence",
			observation.signature,
		)
	}

	encoded := strings.TrimSuffix(
		strings.TrimPrefix(
			observation.signature,
			signaturePrefix,
		),
		signatureSuffix,
	)

	signature, err :=
		base64.StdEncoding.DecodeString(
			encoded,
		)
	if err != nil {
		t.Fatalf(
			"decode Signature: %v",
			err,
		)
	}

	base :=
		`"@authority": ` +
			authority +
			"\n" +
			`"signature-agent": ` +
			observation.signatureAgent +
			"\n" +
			`"@signature-params": ` +
			parameters

	if !ed25519.Verify(
		identity.PublicKey(),
		[]byte(base),
		signature,
	) {
		t.Fatal(
			"Web Bot Auth signature verification failed",
		)
	}
}
