package robots

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

type webBotAuthIntegrationResolver struct{}

func (webBotAuthIntegrationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
	}, nil
}

type webBotAuthIntegrationDialer struct {
	target string
}

func (dialer webBotAuthIntegrationDialer) DialContext(
	ctx context.Context,
	network string,
	_ string,
) (net.Conn, error) {
	var local net.Dialer

	return local.DialContext(
		ctx,
		network,
		dialer.target,
	)
}

type webBotAuthIntegrationObservation struct {
	authority      string
	path           string
	userAgent      string
	signatureAgent string
	signatureInput string
	signature      string
}

func TestWebBotAuthSharedBoundaryIntegration(
	t *testing.T,
) {
	identity := newWebBotAuthIntegrationIdentity(t)

	signer, err := identity.Signer()
	if err != nil {
		t.Fatalf(
			"Identity.Signer() error = %v, want nil",
			err,
		)
	}

	observations := make(
		[]webBotAuthIntegrationObservation,
		0,
		3,
	)

	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			observations = append(
				observations,
				webBotAuthIntegrationObservation{
					authority: request.Host,
					path:      request.URL.Path,
					userAgent: request.UserAgent(),
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

			switch {
			case request.Host == "example.com" &&
				request.URL.Path == "/robots.txt":
				writer.Header().Set(
					"Location",
					"http://policy.example/robots.txt",
				)
				writer.WriteHeader(http.StatusFound)

			case request.Host == "policy.example" &&
				request.URL.Path == "/robots.txt":
				_, _ = writer.Write(
					[]byte(
						"User-agent: Joshternet-Joshbot\n" +
							"Allow: /\n",
					),
				)

			case request.Host == "example.com" &&
				request.URL.Path == "/page":
				_, _ = writer.Write(
					[]byte("page"),
				)

			default:
				http.Error(
					writer,
					"unexpected request",
					http.StatusNotFound,
				)
			}
		}),
	)
	server.Start()
	t.Cleanup(server.Close)

	checker := NewCheckerWithRequestDelayAndSigner(
		webBotAuthIntegrationResolver{},
		webBotAuthIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
		0,
		signer,
	)

	target, err := url.Parse(
		"http://example.com/page",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v, want nil",
			err,
		)
	}

	response, err := checker.Get(
		context.Background(),
		target,
	)
	if err != nil {
		t.Fatalf(
			"Checker.Get() error = %v, want nil",
			err,
		)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf(
			"read response body: %v",
			err,
		)
	}

	if err := response.Body.Close(); err != nil {
		t.Fatalf(
			"close response body: %v",
			err,
		)
	}

	if got := string(body); got != "page" {
		t.Fatalf(
			"response body = %q, want %q",
			got,
			"page",
		)
	}

	if len(observations) != 3 {
		t.Fatalf(
			"request count = %d, want 3",
			len(observations),
		)
	}

	wantRequests := []struct {
		authority string
		path      string
	}{
		{
			authority: "example.com",
			path:      "/robots.txt",
		},
		{
			authority: "policy.example",
			path:      "/robots.txt",
		},
		{
			authority: "example.com",
			path:      "/page",
		},
	}

	signatureInputs := make(
		map[string]struct{},
		len(observations),
	)

	for index, observation := range observations {
		want := wantRequests[index]

		if observation.authority != want.authority {
			t.Errorf(
				"request %d authority = %q, want %q",
				index,
				observation.authority,
				want.authority,
			)
		}

		if observation.path != want.path {
			t.Errorf(
				"request %d path = %q, want %q",
				index,
				observation.path,
				want.path,
			)
		}

		verifyWebBotAuthIntegrationSignature(
			t,
			identity,
			observation,
		)

		signatureInputs[observation.signatureInput] =
			struct{}{}
	}

	if len(signatureInputs) != len(observations) {
		t.Fatalf(
			"unique Signature-Input count = %d, want %d",
			len(signatureInputs),
			len(observations),
		)
	}
}

func newWebBotAuthIntegrationIdentity(
	t *testing.T,
) *webbotauth.Identity {
	t.Helper()

	seed := make(
		[]byte,
		ed25519.SeedSize,
	)
	for index := range seed {
		seed[index] = byte(index + 1)
	}

	privateKey := ed25519.NewKeyFromSeed(seed)

	der, err := x509.MarshalPKCS8PrivateKey(
		privateKey,
	)
	if err != nil {
		t.Fatalf(
			"MarshalPKCS8PrivateKey() error = %v, want nil",
			err,
		)
	}

	privateKeyPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		},
	)
	if privateKeyPEM == nil {
		t.Fatal(
			"pem.EncodeToMemory() returned nil",
		)
	}

	identity, err := webbotauth.ReadIdentity(
		bytes.NewReader(privateKeyPEM),
	)
	if err != nil {
		t.Fatalf(
			"webbotauth.ReadIdentity() error = %v, want nil",
			err,
		)
	}

	return identity
}

func verifyWebBotAuthIntegrationSignature(
	t *testing.T,
	identity *webbotauth.Identity,
	observation webBotAuthIntegrationObservation,
) {
	t.Helper()

	if observation.userAgent != UserAgent {
		t.Errorf(
			"User-Agent = %q, want %q",
			observation.userAgent,
			UserAgent,
		)
	}

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

	if !strings.HasPrefix(
		parameters,
		`("@authority" "signature-agent")`,
	) {
		t.Errorf(
			"Signature-Input parameters = %q, want signed authority and signature-agent",
			parameters,
		)
	}

	keyID, ok := quotedWebBotAuthParameter(
		parameters,
		"keyid",
	)
	if !ok {
		t.Fatalf(
			"Signature-Input parameters = %q, want keyid",
			parameters,
		)
	}

	if keyID != webBotAuthThumbprint(
		identity.PublicJWK(),
	) {
		t.Errorf(
			"keyid = %q, want JWK thumbprint",
			keyID,
		)
	}

	if !strings.Contains(
		parameters,
		`alg="ed25519"`,
	) {
		t.Errorf(
			"Signature-Input parameters = %q, want Ed25519",
			parameters,
		)
	}

	if !strings.Contains(
		parameters,
		`tag="web-bot-auth"`,
	) {
		t.Errorf(
			"Signature-Input parameters = %q, want web-bot-auth tag",
			parameters,
		)
	}

	const (
		signaturePrefix = "sig1=:"
		signatureSuffix = ":"
	)

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

	encodedSignature := strings.TrimSuffix(
		strings.TrimPrefix(
			observation.signature,
			signaturePrefix,
		),
		signatureSuffix,
	)

	signature, err := base64.StdEncoding.DecodeString(
		encodedSignature,
	)
	if err != nil {
		t.Fatalf(
			"decode Signature: %v",
			err,
		)
	}

	signatureBase :=
		`"@authority": ` +
			observation.authority +
			"\n" +
			`"signature-agent": ` +
			observation.signatureAgent +
			"\n" +
			`"@signature-params": ` +
			parameters

	if !ed25519.Verify(
		identity.PublicKey(),
		[]byte(signatureBase),
		signature,
	) {
		t.Fatalf(
			"signature verification failed for %s%s",
			observation.authority,
			observation.path,
		)
	}

	created, ok := integerWebBotAuthParameter(
		parameters,
		"created",
	)
	expires, expiresOK := integerWebBotAuthParameter(
		parameters,
		"expires",
	)
	nonce, nonceOK := quotedWebBotAuthParameter(
		parameters,
		"nonce",
	)

	if !ok || !expiresOK || expires-created != 60 {
		t.Errorf(
			"signature lifetime created=%d expires=%d, want a 60s window",
			created,
			expires,
		)
	}

	age := time.Since(time.Unix(created, 0))
	if age < -time.Minute || age > 2*time.Minute {
		t.Errorf(
			"signature created age = %s, want a current timestamp",
			age,
		)
	}

	decodedNonce, err := base64.StdEncoding.DecodeString(
		nonce,
	)
	if !nonceOK || err != nil || len(decodedNonce) != 32 {
		t.Errorf(
			"nonce = %q, want 32 bytes",
			nonce,
		)
	}
}

func webBotAuthThumbprint(
	jwk webbotauth.PublicJWK,
) string {
	canonical :=
		`{"crv":"` + jwk.Crv +
			`","kty":"` + jwk.Kty +
			`","x":"` + jwk.X +
			`"}`
	sum := sha256.Sum256([]byte(canonical))

	return base64.RawURLEncoding.EncodeToString(
		sum[:],
	)
}

func quotedWebBotAuthParameter(
	parameters string,
	name string,
) (string, bool) {
	token := ";" + name + `="`
	start := strings.Index(parameters, token)
	if start < 0 {
		return "", false
	}

	start += len(token)
	end := strings.Index(parameters[start:], `"`)
	if end < 0 {
		return "", false
	}

	return parameters[start : start+end], true
}

func integerWebBotAuthParameter(
	parameters string,
	name string,
) (int64, bool) {
	token := ";" + name + "="
	start := strings.Index(parameters, token)
	if start < 0 {
		return 0, false
	}

	start += len(token)
	end := start
	for end < len(parameters) &&
		parameters[end] >= '0' &&
		parameters[end] <= '9' {
		end++
	}

	if end == start {
		return 0, false
	}

	value, err := strconv.ParseInt(
		parameters[start:end],
		10,
		64,
	)
	if err != nil {
		return 0, false
	}

	return value, true
}
