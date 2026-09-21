package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/webbotauth"
)

func TestActiveWebBotAuthKeySignsThroughChecker(t *testing.T) {
	identityA, pathA :=
		writeWebBotAuthPrivateKeyWithIdentity(t)
	identityB, pathB :=
		writeWebBotAuthPrivateKeyWithIdentity(t)

	signThroughChecker := func(
		t *testing.T,
		environment mapEnvironment,
	) *http.Request {
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

		var signed *http.Request

		server := httptest.NewUnstartedServer(
			http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				if request.URL.Path == "/page" {
					copied := request.Clone(
						context.Background(),
					)
					signed = copied
				}

				if request.URL.Path == "/robots.txt" {
					_, _ = writer.Write([]byte(
						"User-agent: " +
							robots.ProductToken +
							"\nAllow: /\n",
					))

					return
				}

				_, _ = writer.Write([]byte("page"))
			}),
		)
		server.Start()
		t.Cleanup(server.Close)

		dialer := &webBotAuthRotationDialer{
			target: server.Listener.Addr().String(),
		}

		checker := robots.NewCheckerWithRequestDelayAndSigner(
			webBotAuthRotationResolver{},
			dialer,
			0,
			signer,
		)

		target, err := url.Parse(
			"http://example.com/page",
		)
		if err != nil {
			t.Fatal(err)
		}

		response, err := checker.Get(
			context.Background(),
			target,
		)
		if err != nil {
			t.Fatalf(
				"Checker.Get() error = %v",
				err,
			)
		}

		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}

		if signed == nil {
			t.Fatal("protected request was not signed")
		}

		return signed
	}

	assertActiveSignature := func(
		t *testing.T,
		environment mapEnvironment,
		active *webbotauth.Identity,
		inactive *webbotauth.Identity,
	) {
		t.Helper()

		request := signThroughChecker(
			t,
			environment,
		)
		activeID := webBotAuthRotationThumbprint(
			t,
			active.PublicJWK(),
		)
		inactiveID := webBotAuthRotationThumbprint(
			t,
			inactive.PublicJWK(),
		)

		input := request.Header.Get("Signature-Input")
		if !strings.Contains(
			input,
			`keyid="`+activeID+`"`,
		) {
			t.Fatalf(
				"Signature-Input = %q, want active key %s",
				input,
				activeID,
			)
		}

		if strings.Contains(
			input,
			`keyid="`+inactiveID+`"`,
		) {
			t.Fatalf(
				"Signature-Input = %q, transition key %s must not sign",
				input,
				inactiveID,
			)
		}

		signature := webBotAuthRotationSignature(
			t,
			request,
		)
		base := webBotAuthRotationBase(request)

		if !ed25519.Verify(
			active.PublicKey(),
			[]byte(base),
			signature,
		) {
			t.Fatal("active public key did not verify the request")
		}

		if ed25519.Verify(
			inactive.PublicKey(),
			[]byte(base),
			signature,
		) {
			t.Fatal("transition public key verified the request")
		}
	}

	t.Run("A active and B transition", func(t *testing.T) {
		assertActiveSignature(
			t,
			mapEnvironment{
				webBotAuthModeEnvironment:                     webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment:     pathA,
				webBotAuthTransitionPrivateKeyFileEnvironment: pathB,
			},
			identityA,
			identityB,
		)
	})

	t.Run("B active and A transition", func(t *testing.T) {
		assertActiveSignature(
			t,
			mapEnvironment{
				webBotAuthModeEnvironment:                     webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment:     pathB,
				webBotAuthTransitionPrivateKeyFileEnvironment: pathA,
			},
			identityB,
			identityA,
		)
	})

	t.Run("removing transition leaves B signing", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: pathB,
		}

		assertActiveSignature(
			t,
			environment,
			identityB,
			identityA,
		)
		assertActiveSignature(
			t,
			environment,
			identityB,
			identityA,
		)
	})

	t.Run("duplicate transition does not dial", func(t *testing.T) {
		assertWebBotAuthRotationDoesNotDial(
			t,
			mapEnvironment{
				webBotAuthModeEnvironment:                     webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment:     pathA,
				webBotAuthTransitionPrivateKeyFileEnvironment: pathA,
			},
			errDuplicateWebBotAuthIdentities,
		)
	})

	t.Run("malformed transition does not dial", func(t *testing.T) {
		assertWebBotAuthRotationDoesNotDial(
			t,
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: pathA,
				webBotAuthTransitionPrivateKeyFileEnvironment: writeWebBotAuthFile(
					t,
					[]byte("not a private key"),
				),
			},
			errInvalidWebBotAuthPrivateKey,
		)
	})
}

func assertWebBotAuthRotationDoesNotDial(
	t *testing.T,
	environment mapEnvironment,
	want error,
) {
	t.Helper()

	dialer := &webBotAuthRotationDialer{}
	signer, err := loadWebBotAuthSigner(
		environment.get,
	)
	if err == nil {
		checker := robots.NewCheckerWithRequestDelayAndSigner(
			webBotAuthRotationResolver{},
			dialer,
			0,
			signer,
		)
		target, parseErr := url.Parse(
			"http://example.com/page",
		)
		if parseErr != nil {
			t.Fatal(parseErr)
		}

		_, _ = checker.Get(
			context.Background(),
			target,
		)
	}

	if dialer.dials != 0 {
		t.Fatalf(
			"dial count = %d, want 0",
			dialer.dials,
		)
	}

	if !errors.Is(err, want) {
		t.Fatalf(
			"loadWebBotAuthSigner() error = %v, want %v",
			err,
			want,
		)
	}
}

func webBotAuthRotationThumbprint(
	t *testing.T,
	jwk webbotauth.PublicJWK,
) string {
	t.Helper()

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

func webBotAuthRotationSignature(
	t *testing.T,
	request *http.Request,
) []byte {
	t.Helper()

	value := request.Header.Get("Signature")
	encoded := strings.TrimSuffix(
		strings.TrimPrefix(value, "sig1=:"),
		":",
	)

	signature, err := base64.StdEncoding.DecodeString(
		encoded,
	)
	if err != nil {
		t.Fatalf("decode Signature: %v", err)
	}

	return signature
}

func webBotAuthRotationBase(
	request *http.Request,
) string {
	parameters := strings.TrimPrefix(
		request.Header.Get("Signature-Input"),
		"sig1=",
	)

	return `"@authority": example.com` + "\n" +
		`"signature-agent": ` +
		request.Header.Get("Signature-Agent") +
		"\n" +
		`"@signature-params": ` +
		parameters
}

type webBotAuthRotationResolver struct{}

func (webBotAuthRotationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
	}, nil
}

type webBotAuthRotationDialer struct {
	target string
	dials  int
}

func (dialer *webBotAuthRotationDialer) DialContext(
	ctx context.Context,
	network string,
	_ string,
) (net.Conn, error) {
	dialer.dials++

	var local net.Dialer

	return local.DialContext(
		ctx,
		network,
		dialer.target,
	)
}

var _ netguard.Resolver = webBotAuthRotationResolver{}
var _ netguard.Dialer = (*webBotAuthRotationDialer)(nil)
