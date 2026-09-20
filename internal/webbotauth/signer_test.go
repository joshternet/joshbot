package webbotauth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignerConstructsCloudflareWebBotAuthHeaders(t *testing.T) {
	privateKey := testPrivateKey()
	keyID := testKeyID()
	fixedTime := time.Unix(1_735_689_600, 987_654_321).UTC()
	nonceBytes := bytes.Repeat([]byte{0xa5}, nonceSize)
	nonce := base64.StdEncoding.EncodeToString(nonceBytes)

	signer, err := newSigner(
		privateKey,
		keyID,
		bytes.NewReader(nonceBytes),
		func() time.Time { return fixedTime },
	)
	if err != nil {
		t.Fatalf("newSigner() error = %v, want nil", err)
	}

	request, err := http.NewRequest(
		http.MethodGet,
		"https://EXAMPLE.com:443/path?query=value",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("Signature-Agent", "old-agent")
	request.Header.Set("Signature-Input", "old-input")
	request.Header.Set("Signature", "old-signature")

	if err := signer.Sign(request); err != nil {
		t.Fatalf("Signer.Sign() error = %v, want nil", err)
	}

	const created int64 = 1_735_689_600
	const expires int64 = created + 60

	wantAgent := `"` + SignatureAgentURL + `"`
	wantParameters := `("@authority" "signature-agent");created=1735689600;keyid="` +
		keyID +
		`";alg="ed25519";expires=1735689660;nonce="` +
		nonce +
		`";tag="web-bot-auth"`
	wantBase := `"@authority": example.com` + "\n" +
		`"signature-agent": ` + wantAgent + "\n" +
		`"@signature-params": ` + wantParameters
	wantSignatureBytes := ed25519.Sign(privateKey, []byte(wantBase))
	wantSignature := "sig1=:" +
		base64.StdEncoding.EncodeToString(wantSignatureBytes) +
		":"

	if got := request.Header.Get("Signature-Agent"); got != wantAgent {
		t.Errorf("Signature-Agent = %q, want %q", got, wantAgent)
	}
	if got := request.Header.Get("Signature-Input"); got != "sig1="+wantParameters {
		t.Errorf("Signature-Input = %q, want %q", got, "sig1="+wantParameters)
	}
	if got := request.Header.Get("Signature"); got != wantSignature {
		t.Errorf("Signature = %q, want %q", got, wantSignature)
	}

	publicKey := privateKey.Public().(ed25519.PublicKey)
	if !ed25519.Verify(publicKey, []byte(wantBase), wantSignatureBytes) {
		t.Fatal("independently reconstructed signature base did not verify")
	}

	if created != fixedTime.Unix() || expires-created != 60 {
		t.Fatal("test timestamp assumptions are invalid")
	}
}

func TestSignerUsesFreshNonceForEachRequest(t *testing.T) {
	firstNonce := bytes.Repeat([]byte{0x11}, nonceSize)
	secondNonce := bytes.Repeat([]byte{0x22}, nonceSize)
	stream := append(append([]byte(nil), firstNonce...), secondNonce...)

	signer, err := newSigner(
		testPrivateKey(),
		testKeyID(),
		bytes.NewReader(stream),
		func() time.Time { return time.Unix(100, 0).UTC() },
	)
	if err != nil {
		t.Fatal(err)
	}

	first := mustRequest(t, "https://example.com/one")
	second := mustRequest(t, "https://example.com/two")
	if err := signer.Sign(first); err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(second); err != nil {
		t.Fatal(err)
	}

	firstInput := first.Header.Get("Signature-Input")
	secondInput := second.Header.Get("Signature-Input")
	if firstInput == secondInput {
		t.Fatal("Signature-Input reused a nonce")
	}
	if !strings.Contains(
		firstInput,
		`nonce="`+base64.StdEncoding.EncodeToString(firstNonce)+`"`,
	) {
		t.Errorf("first Signature-Input = %q, want first nonce", firstInput)
	}
	if !strings.Contains(
		secondInput,
		`nonce="`+base64.StdEncoding.EncodeToString(secondNonce)+`"`,
	) {
		t.Errorf("second Signature-Input = %q, want second nonce", secondInput)
	}
	if first.Header.Get("Signature") == second.Header.Get("Signature") {
		t.Error("Signature reused across distinct nonces")
	}
}

func TestSignerInitializesNilHeader(t *testing.T) {
	signer := mustTestSigner(t)
	request := mustRequest(t, "https://example.com/page")
	request.Header = nil

	if err := signer.Sign(request); err != nil {
		t.Fatalf("Signer.Sign() error = %v, want nil", err)
	}
	if request.Header == nil {
		t.Fatal("Signer.Sign() left Header nil")
	}
	for _, name := range []string{
		"Signature-Agent",
		"Signature-Input",
		"Signature",
	} {
		if request.Header.Get(name) == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestSignerDoesNotMutateHeadersWhenNonceGenerationFails(t *testing.T) {
	nonceFailure := errors.New("test nonce failure")
	signer, err := newSigner(
		testPrivateKey(),
		testKeyID(),
		failingNonceReader{err: nonceFailure},
		func() time.Time { return time.Unix(100, 0).UTC() },
	)
	if err != nil {
		t.Fatal(err)
	}

	request := mustRequest(t, "https://example.com/page")
	request.Header.Set("Signature-Agent", "original-agent")
	request.Header.Set("Signature-Input", "original-input")
	request.Header.Set("Signature", "original-signature")

	err = signer.Sign(request)
	if !errors.Is(err, ErrNonceGeneration) || !errors.Is(err, nonceFailure) {
		t.Fatalf("Signer.Sign() error = %v, want nonce generation failure", err)
	}

	want := map[string]string{
		"Signature-Agent": "original-agent",
		"Signature-Input": "original-input",
		"Signature":       "original-signature",
	}
	for name, value := range want {
		if got := request.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestSignerFormattingAndLogsDoNotLeakPrivateKey(t *testing.T) {
	privateKey := testPrivateKey()
	keyID := testKeyID()

	signer, err := newSigner(
		privateKey,
		keyID,
		bytes.NewReader(make([]byte, nonceSize)),
		func() time.Time { return time.Unix(100, 0).UTC() },
	)
	if err != nil {
		t.Fatal(err)
	}

	secretForms := []string{
		base64.RawURLEncoding.EncodeToString(
			privateKey[:ed25519.SeedSize],
		),
		base64.StdEncoding.EncodeToString(privateKey),
		fmt.Sprintf("%x", privateKey),
	}

	rendered := fmt.Sprintf(
		"%v %+v %#v %s %q",
		signer,
		signer,
		signer,
		signer,
		signer,
	)

	if !strings.Contains(rendered, keyID) {
		t.Error("formatted signer omitted public key identifier")
	}

	if !strings.Contains(rendered, "private_key:<redacted>") {
		t.Error("formatted signer omitted private-key redaction marker")
	}

	var logBuffer bytes.Buffer
	logger := slog.New(
		slog.NewTextHandler(
			&logBuffer,
			nil,
		),
	)

	logger.Info(
		"signer",
		"web_bot_auth_signer",
		signer,
	)

	rendered += logBuffer.String()

	for _, secret := range secretForms {
		if strings.Contains(rendered, secret) {
			t.Fatal(
				"formatted or logged signer leaked private key material",
			)
		}
	}

	marshaled, err := json.Marshal(
		map[string]any{"signer": signer},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, secret := range secretForms {
		if strings.Contains(string(marshaled), secret) {
			t.Fatal(
				"JSON signer dump leaked private key material",
			)
		}
	}
}

func TestNilSignerFormattingAndLogging(t *testing.T) {
	var signer *Signer

	if got := fmt.Sprintf("%v", signer); got != "<nil>" {
		t.Errorf(
			"nil formatted signer = %q, want <nil>",
			got,
		)
	}

	if got := signer.LogValue().String(); got != "<nil>" {
		t.Errorf(
			"nil signer log value = %q, want <nil>",
			got,
		)
	}
}

func TestNewSignerValidatesInputs(t *testing.T) {
	validKey := testPrivateKey()
	validKeyID := testKeyID()

	tests := []struct {
		name      string
		key       ed25519.PrivateKey
		keyID     string
		nonce     io.Reader
		now       func() time.Time
		wantError error
	}{
		{
			name:      "short private key",
			key:       ed25519.PrivateKey(make([]byte, ed25519.PrivateKeySize-1)),
			keyID:     validKeyID,
			nonce:     bytes.NewReader(make([]byte, nonceSize)),
			now:       time.Now,
			wantError: ErrInvalidSigningKey,
		},
		{
			name:      "short key id",
			key:       validKey,
			keyID:     "short",
			nonce:     bytes.NewReader(make([]byte, nonceSize)),
			now:       time.Now,
			wantError: ErrInvalidKeyID,
		},
		{
			name:      "invalid base64url key id",
			key:       validKey,
			keyID:     strings.Repeat("!", 43),
			nonce:     bytes.NewReader(make([]byte, nonceSize)),
			now:       time.Now,
			wantError: ErrInvalidKeyID,
		},
		{
			name:      "nil nonce source",
			key:       validKey,
			keyID:     validKeyID,
			nonce:     nil,
			now:       time.Now,
			wantError: errNonceSourceUnavailable,
		},
		{
			name:      "nil clock",
			key:       validKey,
			keyID:     validKeyID,
			nonce:     bytes.NewReader(make([]byte, nonceSize)),
			now:       nil,
			wantError: errClockUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signer, err := newSigner(
				test.key,
				test.keyID,
				test.nonce,
				test.now,
			)
			if signer != nil {
				t.Errorf("newSigner() signer = %#v, want nil", signer)
			}
			if !errors.Is(err, test.wantError) {
				t.Errorf("newSigner() error = %v, want %v", err, test.wantError)
			}
		})
	}

	signer, err := NewSigner(validKey, validKeyID)
	if err != nil {
		t.Fatalf("NewSigner() error = %v, want nil", err)
	}
	if signer == nil || signer.nonce == nil || signer.now == nil {
		t.Fatal("NewSigner() omitted production dependencies")
	}

	validKey[0] ^= 0xff
	if bytes.Equal(signer.privateKey, validKey) {
		t.Error("NewSigner() retained mutable caller private-key storage")
	}
}

func TestSignerRejectsInvalidRequests(t *testing.T) {
	signer := mustTestSigner(t)

	tests := []struct {
		name    string
		request *http.Request
	}{
		{
			name:    "nil request",
			request: nil,
		},
		{
			name: "nil URL",
			request: &http.Request{
				Method: http.MethodGet,
			},
		},
		{
			name: "unsupported scheme",
			request: &http.Request{
				Method: http.MethodGet,
				URL: &url.URL{
					Scheme: "file",
					Path:   "/tmp/page",
				},
			},
		},
		{
			name: "missing host",
			request: &http.Request{
				Method: http.MethodGet,
				URL: &url.URL{
					Scheme: "https",
					Path:   "/page",
				},
			},
		},
		{
			name: "invalid Host override",
			request: func() *http.Request {
				request := mustRequest(t, "https://example.com/page")
				request.Host = "user:password@example.com"
				return request
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := signer.Sign(test.request)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("Signer.Sign() error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	var absent *Signer
	if err := absent.Sign(
		mustRequest(t, "https://example.com/page"),
	); !errors.Is(err, ErrSignerUnavailable) {
		t.Errorf("nil Signer.Sign() error = %v, want ErrSignerUnavailable", err)
	}
}

func TestRequestAuthorityCanonicalization(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		host string
		want string
	}{
		{
			name: "HTTPS default port omitted",
			raw:  "https://EXAMPLE.com:443/page",
			want: "example.com",
		},
		{
			name: "HTTP default port omitted",
			raw:  "http://EXAMPLE.com:80/page",
			want: "example.com",
		},
		{
			name: "non-default port retained",
			raw:  "https://EXAMPLE.com:8443/page",
			want: "example.com:8443",
		},
		{
			name: "IPv6 authority canonicalized",
			raw:  "https://[2001:0db8::1]:443/page",
			want: "[2001:db8::1]",
		},
		{
			name: "Host override controls wire authority",
			raw:  "https://ignored.example/page",
			host: "EXAMPLE.com:8443",
			want: "example.com:8443",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mustRequest(t, test.raw)
			request.Host = test.host

			got, err := requestAuthority(request)
			if err != nil {
				t.Fatalf("requestAuthority() error = %v, want nil", err)
			}
			if got != test.want {
				t.Errorf("authority = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSignatureHelpers(t *testing.T) {
	keyID := testKeyID()
	if !validKeyID(keyID) {
		t.Fatalf("validKeyID(%q) = false, want true", keyID)
	}
	if validKeyID(strings.Repeat("A", 42)) {
		t.Error("validKeyID() accepted wrong length")
	}
	if validKeyID(strings.Repeat("!", 43)) {
		t.Error("validKeyID() accepted malformed base64url")
	}

	parameters := signatureParameters(
		10,
		70,
		keyID,
		"nonce-value",
	)
	wantParameters := `("@authority" "signature-agent");created=10;keyid="` +
		keyID +
		`";alg="ed25519";expires=70;nonce="nonce-value";tag="web-bot-auth"`
	if parameters != wantParameters {
		t.Errorf("signatureParameters() = %q, want %q", parameters, wantParameters)
	}

	base := signatureBase(
		"example.com",
		`"https://agent.example"`,
		parameters,
	)
	wantBase := `"@authority": example.com` + "\n" +
		`"signature-agent": "https://agent.example"` + "\n" +
		`"@signature-params": ` + parameters
	if base != wantBase {
		t.Errorf("signatureBase() = %q, want %q", base, wantBase)
	}
}

func mustTestSigner(t *testing.T) *Signer {
	t.Helper()

	signer, err := newSigner(
		testPrivateKey(),
		testKeyID(),
		bytes.NewReader(make([]byte, nonceSize*8)),
		func() time.Time { return time.Unix(100, 0).UTC() },
	)
	if err != nil {
		t.Fatalf("newSigner() error = %v, want nil", err)
	}
	return signer
}

func testPrivateKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func testKeyID() string {
	value := make([]byte, 32)
	for index := range value {
		value[index] = byte(255 - index)
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func mustRequest(
	t *testing.T,
	raw string,
) *http.Request {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("http.NewRequest(%q) error = %v", raw, err)
	}
	return request
}

type failingNonceReader struct {
	err error
}

func (reader failingNonceReader) Read([]byte) (int, error) {
	return 0, reader.err
}
