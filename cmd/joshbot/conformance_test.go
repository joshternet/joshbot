package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

func TestWebBotAuthConformanceCommand(t *testing.T) {
	t.Run("usage", func(t *testing.T) {
		for _, args := range [][]string{
			{"conformance"},
			{
				"conformance",
				"web-bot-auth",
			},
			{
				"conformance",
				"web-bot-auth",
				"--expect",
				"maybe",
			},
			{
				"conformance",
				"signed-get",
				"--expect",
				"unregistered",
			},
		} {
			var stdout, stderr bytes.Buffer
			code := runWithOperations(
				context.Background(),
				args,
				&stdout,
				&stderr,
				newRuntimeOperations(io.Discard),
			)
			if code != exitUsage {
				t.Fatalf(
					"run(%v) = %d, want usage",
					args,
					code,
				)
			}
		}
	})

	_, active, activePath :=
		writeConformanceIdentity(t)
	_, transition, transitionPath :=
		writeConformanceIdentity(t)
	directorySignature := ""

	client := &conformanceClient{
		directory: func() *http.Response {
			header, body, signature :=
				signedConformanceDirectory(
					t,
					time.Now(),
					active,
					transition,
				)
			directorySignature = signature

			return conformanceResponse(
				http.StatusOK,
				header,
				body,
			)
		},
		probeStatus: http.StatusUnauthorized,
	}

	environment := mapEnvironment{
		webBotAuthModeEnvironment:                     webBotAuthModeRequired,
		webBotAuthActivePrivateKeyFileEnvironment:     activePath,
		webBotAuthTransitionPrivateKeyFileEnvironment: transitionPath,
	}

	var stdout, stderr bytes.Buffer
	code := runWebBotAuthConformance(
		context.Background(),
		[]string{
			"web-bot-auth",
			"--expect",
			"unregistered",
		},
		&stdout,
		&stderr,
		environment.get,
		client,
	)
	if code != exitSuccess {
		t.Fatalf(
			"conformance = %d, stderr %s",
			code,
			stderr.String(),
		)
	}

	output := stdout.String()
	for _, want := range []string{
		"directory: " + webbotauth.SignatureAgentURL,
		"keys: 2",
		"state: " + webbotauth.DirectoryStateRotationOverlap,
		"active key: " + active.KeyID(),
		"transition key: " + transition.KeyID(),
		"cloudflare: 401",
		"pass: Cloudflare accepted the Web Bot Auth request shape and did not verify the identity.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf(
				"stdout missing %q\n%s",
				want,
				output,
			)
		}
	}

	if strings.Contains(output, directorySignature) ||
		strings.Contains(output, "PRIVATE KEY") ||
		strings.Contains(stderr.String(), activePath) {
		t.Fatalf(
			"output leaked signature material or the key path\nstdout: %s\nstderr: %s",
			output,
			stderr.String(),
		)
	}

	if len(client.calls) != 2 ||
		client.calls[0] != webbotauth.SignatureAgentURL ||
		client.calls[1] != cloudflareWebBotAuthTestURL {
		t.Fatalf(
			"calls = %v, want directory then Cloudflare",
			client.calls,
		)
	}

	t.Run("verified", func(t *testing.T) {
		verified := *client
		verified.calls = nil
		verified.probeStatus = http.StatusOK
		verified.directory = func() *http.Response {
			header, body, _ := signedConformanceDirectory(
				t,
				time.Now(),
				active,
			)

			return conformanceResponse(
				http.StatusOK,
				header,
				body,
			)
		}

		var out, errOut bytes.Buffer
		code := runWebBotAuthConformance(
			context.Background(),
			[]string{
				"web-bot-auth",
				"--expect",
				"verified",
			},
			&out,
			&errOut,
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			}.get,
			&verified,
		)
		if code != exitSuccess {
			t.Fatalf(
				"conformance = %d, stderr %s",
				code,
				errOut.String(),
			)
		}

		if !strings.Contains(
			out.String(),
			"pass: Cloudflare verified the Web Bot Auth request.",
		) || strings.Contains(out.String(), "transition key:") {
			t.Fatalf("stdout = %s", out.String())
		}
	})

	failures := []struct {
		name        string
		environment mapEnvironment
		client      *conformanceClient
		args        []string
		ctx         context.Context
		want        string
		wantCalls   int
		signerErr   error
	}{
		{
			name: "unsigned mode",
			environment: mapEnvironment{
				webBotAuthModeEnvironment: webBotAuthModeUnsigned,
			},
			client:    &conformanceClient{},
			want:      "unsigned mode is not allowed",
			wantCalls: 0,
		},
		{
			name:        "missing active key",
			environment: mapEnvironment{},
			client:      &conformanceClient{},
			want:        errInvalidWebBotAuthConfiguration.Error(),
			wantCalls:   0,
		},
		{
			name: "malformed active key",
			environment: mapEnvironment{
				webBotAuthActivePrivateKeyFileEnvironment: writeWebBotAuthFile(
					t,
					[]byte("not a private key"),
				),
			},
			client:    &conformanceClient{},
			want:      errInvalidWebBotAuthPrivateKey.Error(),
			wantCalls: 0,
		},
		{
			name: "invalid request delay",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
				crawlRequestDelayEnvironment:              "nope",
			},
			client:    &conformanceClient{},
			want:      "non-negative duration",
			wantCalls: 0,
		},
		{
			name: "directory validation failure",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					return conformanceResponse(
						http.StatusOK,
						http.Header{
							"Content-Type": []string{
								webbotauth.DirectoryContentType,
							},
						},
						[]byte(`{"keys":[]}`),
					)
				},
				probeStatus: http.StatusUnauthorized,
			},
			want:      webbotauth.ErrInvalidDirectory.Error(),
			wantCalls: 1,
		},
		{
			name: "active key is not published",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					header, body, _ :=
						signedConformanceDirectory(
							t,
							time.Now(),
							transition,
						)

					return conformanceResponse(
						http.StatusOK,
						header,
						body,
					)
				},
			},
			want:      "active key",
			wantCalls: 1,
		},
		{
			name: "transition key is not published",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                     webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment:     activePath,
				webBotAuthTransitionPrivateKeyFileEnvironment: transitionPath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					header, body, _ :=
						signedConformanceDirectory(
							t,
							time.Now(),
							active,
						)

					return conformanceResponse(
						http.StatusOK,
						header,
						body,
					)
				},
			},
			want:      "transition key",
			wantCalls: 1,
		},
		{
			name: "malformed request",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: conformanceProbeClient(
				t,
				active,
				http.StatusBadRequest,
			),
			want:      "malformed",
			wantCalls: 2,
		},
		{
			name: "unexpected status",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: conformanceProbeClient(
				t,
				active,
				http.StatusTeapot,
			),
			want:      "HTTP 418",
			wantCalls: 2,
		},
		{
			name: "verified expectation gets 401",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			args: []string{
				"web-bot-auth",
				"--expect",
				"verified",
			},
			client: conformanceProbeClient(
				t,
				active,
				http.StatusUnauthorized,
			),
			want:      "did not verify",
			wantCalls: 2,
		},
		{
			name: "unregistered expectation gets 200",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: conformanceProbeClient(
				t,
				active,
				http.StatusOK,
			),
			want:      "before registration was expected",
			wantCalls: 2,
		},
		{
			name: "canceled",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{},
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(
					context.Background(),
				)
				cancel()

				return ctx
			}(),
			want:      context.Canceled.Error(),
			wantCalls: 0,
		},
		{
			name: "probe network failure",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					header, body, _ :=
						signedConformanceDirectory(
							t,
							time.Now(),
							active,
						)

					return conformanceResponse(
						http.StatusOK,
						header,
						body,
					)
				},
				probeErr: errors.New("probe dial failed"),
			},
			want:      "probe dial failed",
			wantCalls: 2,
		},
		{
			name: "network failure",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				err: errors.New("dial failed"),
			},
			want:      "dial failed",
			wantCalls: 1,
		},
		{
			name: "empty response",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					return &http.Response{
						StatusCode: http.StatusOK,
					}
				},
			},
			want:      "empty response",
			wantCalls: 1,
		},
		{
			name: "response is too large",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					return conformanceResponse(
						http.StatusOK,
						nil,
						bytes.Repeat(
							[]byte("x"),
							webbotauth.MaxDirectoryBodySize+1,
						),
					)
				},
			},
			want:      "too large",
			wantCalls: 1,
		},
		{
			name: "response read fails",
			environment: mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			client: &conformanceClient{
				directory: func() *http.Response {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       conformanceFailBody{},
						Header:     make(http.Header),
					}
				},
			},
			want:      "read failed",
			wantCalls: 1,
		},
	}

	for _, test := range failures {
		t.Run(test.name, func(t *testing.T) {
			if test.signerErr != nil {
				original := webBotAuthConformanceSigner
				t.Cleanup(func() {
					webBotAuthConformanceSigner = original
				})
				webBotAuthConformanceSigner = func(
					*webbotauth.Identity,
				) (*webbotauth.Signer, error) {
					return nil, test.signerErr
				}
			}

			args := test.args
			if args == nil {
				args = []string{
					"web-bot-auth",
					"--expect",
					"unregistered",
				}
			}

			ctx := test.ctx
			if ctx == nil {
				ctx = context.Background()
			}

			var out, errOut bytes.Buffer
			code := runWebBotAuthConformance(
				ctx,
				args,
				&out,
				&errOut,
				test.environment.get,
				test.client,
			)
			if code != exitFailure {
				t.Fatalf(
					"conformance = %d, want failure, stdout %s",
					code,
					out.String(),
				)
			}

			if !strings.Contains(
				errOut.String(),
				test.want,
			) {
				t.Fatalf(
					"stderr = %q, want %q",
					errOut.String(),
					test.want,
				)
			}

			if len(test.client.calls) != test.wantCalls {
				t.Fatalf(
					"calls = %v, want %d",
					test.client.calls,
					test.wantCalls,
				)
			}
		})
	}

	t.Run("signer construction failure", func(t *testing.T) {
		original := webBotAuthConformanceSigner
		t.Cleanup(func() {
			webBotAuthConformanceSigner = original
		})
		webBotAuthConformanceSigner = func(
			*webbotauth.Identity,
		) (*webbotauth.Signer, error) {
			return nil, errors.New("signer failed")
		}

		client := &conformanceClient{}
		var out, errOut bytes.Buffer
		code := runWebBotAuthConformance(
			context.Background(),
			[]string{
				"web-bot-auth",
				"--expect",
				"unregistered",
			},
			&out,
			&errOut,
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			}.get,
			client,
		)
		if code != exitFailure ||
			!strings.Contains(errOut.String(), "signer failed") ||
			len(client.calls) != 0 {
			t.Fatalf(
				"code %d calls %v stderr %s",
				code,
				client.calls,
				errOut.String(),
			)
		}
	})

	t.Run("canceled during the request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		client := &conformanceClient{
			err:    errors.New("dial failed"),
			cancel: cancel,
		}
		var out, errOut bytes.Buffer
		code := runWebBotAuthConformance(
			ctx,
			[]string{
				"web-bot-auth",
				"--expect",
				"unregistered",
			},
			&out,
			&errOut,
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			}.get,
			client,
		)
		if code != exitFailure ||
			!strings.Contains(
				errOut.String(),
				context.Canceled.Error(),
			) {
			t.Fatalf(
				"stderr = %q, want cancellation",
				errOut.String(),
			)
		}
	})

	t.Run("production client stops before dial when canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()

		var out, errOut bytes.Buffer
		code := runWebBotAuthConformance(
			ctx,
			[]string{
				"web-bot-auth",
				"--expect",
				"unregistered",
			},
			&out,
			&errOut,
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			}.get,
			nil,
		)
		if code != exitFailure ||
			!strings.Contains(
				errOut.String(),
				context.Canceled.Error(),
			) {
			t.Fatalf(
				"stderr = %q, want cancellation",
				errOut.String(),
			)
		}
	})

	t.Run("fixed targets stay HTTPS", func(t *testing.T) {
		originalDirectory := webBotAuthConformanceDirectoryURL
		originalProbe := webBotAuthConformanceProbeURL
		t.Cleanup(func() {
			webBotAuthConformanceDirectoryURL = originalDirectory
			webBotAuthConformanceProbeURL = originalProbe
		})

		environment := mapEnvironment{
			webBotAuthModeEnvironment:                 webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment: activePath,
		}
		args := []string{
			"web-bot-auth",
			"--expect",
			"unregistered",
		}

		for _, raw := range []string{
			"http://example.com/directory",
			"https://user:secret@joshternet.org/.well-known/http-message-signatures-directory",
		} {
			webBotAuthConformanceDirectoryURL = raw
			webBotAuthConformanceProbeURL = originalProbe
			client := &conformanceClient{}
			var out, errOut bytes.Buffer
			code := runWebBotAuthConformance(
				context.Background(),
				args,
				&out,
				&errOut,
				environment.get,
				client,
			)
			if code != exitFailure ||
				len(client.calls) != 0 ||
				errOut.Len() == 0 {
				t.Fatalf(
					"directory %s: code %d calls %v stderr %s",
					raw,
					code,
					client.calls,
					errOut.String(),
				)
			}
		}

		webBotAuthConformanceDirectoryURL = originalDirectory
		webBotAuthConformanceProbeURL = "http://crawltest.com/cdn-cgi/web-bot-auth"
		client := &conformanceClient{}
		var out, errOut bytes.Buffer
		code := runWebBotAuthConformance(
			context.Background(),
			args,
			&out,
			&errOut,
			environment.get,
			client,
		)
		if code != exitFailure || len(client.calls) != 0 {
			t.Fatalf(
				"probe: code %d calls %v stderr %s",
				code,
				client.calls,
				errOut.String(),
			)
		}
	})
}

func TestWebBotAuthConformanceTargetParsing(t *testing.T) {
	if _, err := parseFixedHTTPSURL(
		"http://example.com/directory",
	); !errors.Is(err, errWebBotAuthConformance) {
		t.Fatalf(
			"HTTP URL error = %v, want conformance failure",
			err,
		)
	}

	if _, err := parseFixedHTTPSURL("://bad"); err == nil {
		t.Fatal("invalid URL error = nil")
	}

	if _, err := webBotAuthDirectoryAuthority(
		&url.URL{
			Scheme: "https",
			Host:   "userinfo.example",
			User:   url.UserPassword("user", "secret"),
		},
	); err == nil {
		t.Fatal("credential URL error = nil")
	}

	var nilContext context.Context
	if _, err := fetchWebBotAuth(
		nilContext,
		&conformanceClient{},
		&url.URL{Scheme: "https", Host: "example.com"},
		1,
	); !errors.Is(err, errWebBotAuthConformance) {
		t.Fatalf("nil context error = %v", err)
	}

	if _, err := fetchWebBotAuth(
		context.Background(),
		nil,
		&url.URL{Scheme: "https", Host: "example.com"},
		1,
	); !errors.Is(err, errWebBotAuthConformance) {
		t.Fatalf("nil client error = %v", err)
	}

	if _, err := fetchWebBotAuth(
		context.Background(),
		&conformanceClient{},
		nil,
		1,
	); !errors.Is(err, errWebBotAuthConformance) {
		t.Fatalf("nil target error = %v", err)
	}

	if _, err := webBotAuthDirectoryAuthority(nil); !errors.Is(
		err,
		errWebBotAuthConformance,
	) {
		t.Fatalf("nil directory error = %v", err)
	}

	if _, err := webBotAuthConformanceSigner(nil); !errors.Is(
		err,
		errWebBotAuthConformance,
	) {
		t.Fatalf("nil signer error = %v", err)
	}

	directory, probe, err := webBotAuthConformanceTargets()
	if err != nil ||
		directory.String() != webbotauth.SignatureAgentURL ||
		probe.String() != cloudflareWebBotAuthTestURL {
		t.Fatalf(
			"targets = %v %v %v",
			directory,
			probe,
			err,
		)
	}

	originalDirectory := webBotAuthConformanceDirectoryURL
	originalProbe := webBotAuthConformanceProbeURL
	t.Cleanup(func() {
		webBotAuthConformanceDirectoryURL = originalDirectory
		webBotAuthConformanceProbeURL = originalProbe
	})

	webBotAuthConformanceDirectoryURL = "http://example.com/directory"
	if _, _, err := webBotAuthConformanceTargets(); !errors.Is(
		err,
		errWebBotAuthConformance,
	) {
		t.Fatalf("directory target error = %v", err)
	}

	webBotAuthConformanceDirectoryURL = originalDirectory
	webBotAuthConformanceProbeURL = "not a url"
	if _, _, err := webBotAuthConformanceTargets(); !errors.Is(
		err,
		errWebBotAuthConformance,
	) {
		t.Fatalf("probe target error = %v", err)
	}
}

type conformanceClient struct {
	directory   func() *http.Response
	probeStatus int
	err         error
	probeErr    error
	cancel      context.CancelFunc
	calls       []string
}

func (client *conformanceClient) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	client.calls = append(
		client.calls,
		target.String(),
	)

	if client.cancel != nil {
		client.cancel()
	}

	if client.err != nil {
		return nil, client.err
	}

	if target.String() == cloudflareWebBotAuthTestURL {
		if client.probeErr != nil {
			return nil, client.probeErr
		}

		return conformanceResponse(
			client.probeStatus,
			nil,
			[]byte("probe"),
		), nil
	}

	if client.directory == nil {
		return nil, errors.New("missing directory")
	}

	return client.directory(), nil
}

func conformanceProbeClient(
	t *testing.T,
	identity *webbotauth.Identity,
	status int,
) *conformanceClient {
	t.Helper()

	return &conformanceClient{
		directory: func() *http.Response {
			header, body, _ := signedConformanceDirectory(
				t,
				time.Now(),
				identity,
			)

			return conformanceResponse(
				http.StatusOK,
				header,
				body,
			)
		},
		probeStatus: status,
	}
}

func conformanceResponse(
	status int,
	header http.Header,
	body []byte,
) *http.Response {
	if header == nil {
		header = make(http.Header)
	}

	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body: io.NopCloser(
			bytes.NewReader(body),
		),
	}
}

type conformanceFailBody struct{}

func (conformanceFailBody) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (conformanceFailBody) Close() error {
	return nil
}

func writeConformanceIdentity(
	t *testing.T,
) (ed25519.PrivateKey, *webbotauth.Identity, string) {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := webbotauth.NewIdentity(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	path := writeWebBotAuthFile(
		t,
		pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		}),
	)
	conformanceKeys[identity.KeyID()] = append(
		ed25519.PrivateKey(nil),
		privateKey...,
	)

	return privateKey, identity, path
}

var conformanceKeys = map[string]ed25519.PrivateKey{}

func signedConformanceDirectory(
	t *testing.T,
	created time.Time,
	identities ...*webbotauth.Identity,
) (http.Header, []byte, string) {
	t.Helper()

	keys := make(
		[]webbotauth.PublicJWK,
		0,
		len(identities),
	)
	for _, identity := range identities {
		keys = append(keys, identity.PublicJWK())
	}

	body, err := json.Marshal(struct {
		Keys []webbotauth.PublicJWK `json:"keys"`
	}{
		Keys: keys,
	})
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(body)
	digest := "sha-256=:" +
		base64.StdEncoding.EncodeToString(sum[:]) +
		":"
	var inputs []string
	var signatures []string
	expires := created.Add(5 * time.Minute).Unix()

	for index, identity := range identities {
		privateKey := conformanceKeys[identity.KeyID()]
		if privateKey == nil {
			t.Fatal("conformance private key was not retained")
		}

		keyID := conformanceThumbprint(identity.PublicJWK())
		parameters := fmt.Sprintf(
			`("@authority";req "content-digest");created=%d;expires=%d;keyid="%s";alg="ed25519";tag="http-message-signatures-directory"`,
			created.Unix(),
			expires,
			keyID,
		)
		base := "\"@authority\";req: joshternet.org\n" +
			"\"content-digest\": " + digest + "\n" +
			"\"@signature-params\": " + parameters
		signed := ed25519.Sign(privateKey, []byte(base))
		label := "binding" + strconv.Itoa(index)
		inputs = append(inputs, label+"="+parameters)
		signatures = append(
			signatures,
			label+"=:"+
				base64.StdEncoding.EncodeToString(signed)+
				":",
		)
	}

	header := make(http.Header)
	header.Set(
		"Content-Type",
		webbotauth.DirectoryContentType,
	)
	header.Set("Content-Digest", digest)
	header.Set(
		"Signature-Input",
		strings.Join(inputs, ", "),
	)
	signature := strings.Join(signatures, ", ")
	header.Set("Signature", signature)

	return header, body, signature
}

func conformanceThumbprint(
	jwk webbotauth.PublicJWK,
) string {
	canonical :=
		`{"crv":"` + jwk.Crv +
			`","kty":"` + jwk.Kty +
			`","x":"` + jwk.X +
			`"}`
	sum := sha256.Sum256([]byte(canonical))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}
