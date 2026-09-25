package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/webbotauth"
)

func TestConformanceIntegrationSuccessfulStates(
	t *testing.T,
) {
	_, active, activePath :=
		writeConformanceIdentity(t)
	_, transition, transitionPath :=
		writeConformanceIdentity(t)

	t.Run("command usage boundary", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := runWebBotAuthConformanceCommand(
			context.Background(),
			[]string{"web-bot-auth"},
			&stdout,
			&stderr,
		)

		if code != exitUsage {
			t.Fatalf(
				"code = %d, want %d",
				code,
				exitUsage,
			)
		}
	})

	t.Run("invalid expectation", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := runWebBotAuthConformance(
			context.Background(),
			[]string{
				"web-bot-auth",
				"--expect",
				"maybe",
			},
			&stdout,
			&stderr,
			mapEnvironment{}.get,
			&conformanceClient{},
		)

		if code != exitUsage {
			t.Fatalf(
				"code = %d, want %d",
				code,
				exitUsage,
			)
		}
	})

	t.Run(
		"unregistered with rotation overlap",
		func(t *testing.T) {
			client := &conformanceClient{
				directory: func() *http.Response {
					header, body, _ :=
						signedConformanceDirectory(
							t,
							time.Now(),
							active,
							transition,
						)

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
					"code = %d, stderr = %q",
					code,
					stderr.String(),
				)
			}

			output := stdout.String()
			for _, want := range []string{
				"keys: 2",
				"active key: " + active.KeyID(),
				"transition key: " +
					transition.KeyID(),
				"cloudflare: 401",
				"pass: Cloudflare accepted",
			} {
				if !strings.Contains(
					output,
					want,
				) {
					t.Errorf(
						"stdout missing %q:\n%s",
						want,
						output,
					)
				}
			}

			if len(client.calls) != 2 {
				t.Fatalf(
					"client calls = %v, want 2",
					client.calls,
				)
			}
		},
	)

	t.Run(
		"verified without transition",
		func(t *testing.T) {
			client := conformanceProbeClient(
				t,
				active,
				http.StatusOK,
			)

			environment := mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			}

			var stdout, stderr bytes.Buffer
			code := runWebBotAuthConformance(
				context.Background(),
				[]string{
					"web-bot-auth",
					"--expect",
					"verified",
				},
				&stdout,
				&stderr,
				environment.get,
				client,
			)

			if code != exitSuccess {
				t.Fatalf(
					"code = %d, stderr = %q",
					code,
					stderr.String(),
				)
			}

			if !strings.Contains(
				stdout.String(),
				"pass: Cloudflare verified",
			) {
				t.Fatalf(
					"stdout = %q",
					stdout.String(),
				)
			}

			if strings.Contains(
				stdout.String(),
				"transition key:",
			) {
				t.Fatalf(
					"unexpected transition output: %q",
					stdout.String(),
				)
			}
		},
	)
}

func TestConformanceIntegrationConfigurationFailures(
	t *testing.T,
) {
	_, active, activePath :=
		writeConformanceIdentity(t)

	runFailure := func(
		t *testing.T,
		ctx context.Context,
		environment mapEnvironment,
		client webBotAuthConformanceClient,
		want string,
	) {
		t.Helper()

		var stdout, stderr bytes.Buffer
		code := runWebBotAuthConformance(
			ctx,
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

		if code != exitFailure {
			t.Fatalf(
				"code = %d, want %d; stdout = %q",
				code,
				exitFailure,
				stdout.String(),
			)
		}

		if !strings.Contains(
			stderr.String(),
			want,
		) {
			t.Fatalf(
				"stderr = %q, want %q",
				stderr.String(),
				want,
			)
		}
	}

	t.Run("missing required identity", func(t *testing.T) {
		runFailure(
			t,
			context.Background(),
			mapEnvironment{},
			&conformanceClient{},
			errInvalidWebBotAuthConfiguration.Error(),
		)
	})

	t.Run("unsigned mode", func(t *testing.T) {
		runFailure(
			t,
			context.Background(),
			mapEnvironment{
				webBotAuthModeEnvironment: webBotAuthModeUnsigned,
			},
			&conformanceClient{},
			"unsigned mode is not allowed",
		)
	})

	t.Run("signer construction", func(t *testing.T) {
		original := webBotAuthConformanceSigner
		t.Cleanup(func() {
			webBotAuthConformanceSigner = original
		})

		webBotAuthConformanceSigner = func(
			*webbotauth.Identity,
		) (*webbotauth.Signer, error) {
			return nil, errors.New(
				"integration signer failure",
			)
		}

		runFailure(
			t,
			context.Background(),
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			&conformanceClient{},
			"integration signer failure",
		)
	})

	t.Run("request delay", func(t *testing.T) {
		runFailure(
			t,
			context.Background(),
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
				crawlRequestDelayEnvironment:              "invalid-duration",
			},
			&conformanceClient{},
			"non-negative duration",
		)
	})

	t.Run(
		"production client observes cancellation",
		func(t *testing.T) {
			ctx, cancel := context.WithCancel(
				context.Background(),
			)
			cancel()

			runFailure(
				t,
				ctx,
				mapEnvironment{
					webBotAuthModeEnvironment:                 webBotAuthModeRequired,
					webBotAuthActivePrivateKeyFileEnvironment: activePath,
				},
				nil,
				context.Canceled.Error(),
			)
		},
	)

	t.Run("nil context", func(t *testing.T) {
		runFailure(
			t,
			nil,
			mapEnvironment{
				webBotAuthModeEnvironment:                 webBotAuthModeRequired,
				webBotAuthActivePrivateKeyFileEnvironment: activePath,
			},
			conformanceProbeClient(
				t,
				active,
				http.StatusUnauthorized,
			),
			"context",
		)
	})
}

func TestConformanceIntegrationTargetFailures(
	t *testing.T,
) {
	_, _, activePath :=
		writeConformanceIdentity(t)

	environment := mapEnvironment{
		webBotAuthModeEnvironment:                 webBotAuthModeRequired,
		webBotAuthActivePrivateKeyFileEnvironment: activePath,
	}

	run := func(
		t *testing.T,
		directory string,
		probe string,
		want string,
	) {
		t.Helper()

		originalDirectory :=
			webBotAuthConformanceDirectoryURL
		originalProbe :=
			webBotAuthConformanceProbeURL

		t.Cleanup(func() {
			webBotAuthConformanceDirectoryURL =
				originalDirectory
			webBotAuthConformanceProbeURL =
				originalProbe
		})

		webBotAuthConformanceDirectoryURL =
			directory
		webBotAuthConformanceProbeURL =
			probe

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
			&conformanceClient{},
		)

		if code != exitFailure {
			t.Fatalf(
				"code = %d, want failure",
				code,
			)
		}

		if !strings.Contains(
			stderr.String(),
			want,
		) {
			t.Fatalf(
				"stderr = %q, want %q",
				stderr.String(),
				want,
			)
		}
	}

	t.Run("directory must be HTTPS", func(t *testing.T) {
		run(
			t,
			"http://example.com/directory",
			cloudflareWebBotAuthTestURL,
			"HTTPS target is required",
		)
	})

	t.Run("probe must be HTTPS", func(t *testing.T) {
		run(
			t,
			webbotauth.SignatureAgentURL,
			"http://crawltest.com/cdn-cgi/web-bot-auth",
			"HTTPS target is required",
		)
	})

	t.Run(
		"directory authority rejects credentials",
		func(t *testing.T) {
			run(
				t,
				"https://user:secret@joshternet.org/.well-known/http-message-signatures-directory",
				cloudflareWebBotAuthTestURL,
				"credentials are not allowed",
			)
		},
	)
}

func TestConformanceIntegrationDirectoryFailures(
	t *testing.T,
) {
	_, active, activePath :=
		writeConformanceIdentity(t)
	_, transition, transitionPath :=
		writeConformanceIdentity(t)

	baseEnvironment := mapEnvironment{
		webBotAuthModeEnvironment:                 webBotAuthModeRequired,
		webBotAuthActivePrivateKeyFileEnvironment: activePath,
	}

	run := func(
		t *testing.T,
		environment mapEnvironment,
		client *conformanceClient,
		want string,
	) {
		t.Helper()

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

		if code != exitFailure {
			t.Fatalf(
				"code = %d, want failure; stdout = %q",
				code,
				stdout.String(),
			)
		}

		if !strings.Contains(
			stderr.String(),
			want,
		) {
			t.Fatalf(
				"stderr = %q, want %q",
				stderr.String(),
				want,
			)
		}
	}

	t.Run("network failure", func(t *testing.T) {
		run(
			t,
			baseEnvironment,
			&conformanceClient{
				err: errors.New(
					"integration dial failure",
				),
			},
			"integration dial failure",
		)
	})

	t.Run("cancellation during request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		client := &conformanceClient{
			err: errors.New(
				"integration dial failure",
			),
			cancel: cancel,
		}

		var stdout, stderr bytes.Buffer
		code := runWebBotAuthConformance(
			ctx,
			[]string{
				"web-bot-auth",
				"--expect",
				"unregistered",
			},
			&stdout,
			&stderr,
			baseEnvironment.get,
			client,
		)

		if code != exitFailure ||
			!strings.Contains(
				stderr.String(),
				context.Canceled.Error(),
			) {
			t.Fatalf(
				"code = %d stderr = %q",
				code,
				stderr.String(),
			)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		run(
			t,
			baseEnvironment,
			&conformanceClient{
				directory: func() *http.Response {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
					}
				},
			},
			"empty response",
		)
	})

	t.Run("response read failure", func(t *testing.T) {
		run(
			t,
			baseEnvironment,
			&conformanceClient{
				directory: func() *http.Response {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: make(
							http.Header,
						),
						Body: conformanceFailBody{},
					}
				},
			},
			"read failed",
		)
	})

	t.Run("response too large", func(t *testing.T) {
		run(
			t,
			baseEnvironment,
			&conformanceClient{
				directory: func() *http.Response {
					return conformanceResponse(
						http.StatusOK,
						nil,
						bytes.Repeat(
							[]byte("x"),
							webbotauth.MaxDirectoryBodySize+
								1,
						),
					)
				},
			},
			"response is too large",
		)
	})

	t.Run(
		"directory validation failure",
		func(t *testing.T) {
			run(
				t,
				baseEnvironment,
				&conformanceClient{
					directory: func() *http.Response {
						return conformanceResponse(
							http.StatusOK,
							http.Header{
								"Content-Type": {
									webbotauth.DirectoryContentType,
								},
							},
							[]byte(
								`{"keys":[]}`,
							),
						)
					},
				},
				webbotauth.ErrInvalidDirectory.Error(),
			)
		},
	)

	t.Run("active key absent", func(t *testing.T) {
		run(
			t,
			baseEnvironment,
			&conformanceClient{
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
			"active key",
		)
	})

	t.Run("transition key absent", func(t *testing.T) {
		environment := mapEnvironment{
			webBotAuthModeEnvironment:                     webBotAuthModeRequired,
			webBotAuthActivePrivateKeyFileEnvironment:     activePath,
			webBotAuthTransitionPrivateKeyFileEnvironment: transitionPath,
		}

		run(
			t,
			environment,
			&conformanceClient{
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
			"transition key",
		)
	})
}

func TestConformanceIntegrationProbeResults(
	t *testing.T,
) {
	_, active, activePath :=
		writeConformanceIdentity(t)

	environment := mapEnvironment{
		webBotAuthModeEnvironment:                 webBotAuthModeRequired,
		webBotAuthActivePrivateKeyFileEnvironment: activePath,
	}

	tests := []struct {
		name        string
		expectation string
		status      int
		want        string
	}{
		{
			name:        "malformed request",
			expectation: webBotAuthExpectUnregistered,
			status:      http.StatusBadRequest,
			want:        "malformed",
		},
		{
			name:        "verified receives unauthorized",
			expectation: webBotAuthExpectVerified,
			status:      http.StatusUnauthorized,
			want:        "did not verify",
		},
		{
			name:        "unregistered receives verified",
			expectation: webBotAuthExpectUnregistered,
			status:      http.StatusOK,
			want: "before registration was " +
				"expected",
		},
		{
			name:        "unexpected response",
			expectation: webBotAuthExpectUnregistered,
			status:      http.StatusTeapot,
			want:        "HTTP 418",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := conformanceProbeClient(
				t,
				active,
				test.status,
			)

			var stdout, stderr bytes.Buffer
			code := runWebBotAuthConformance(
				context.Background(),
				[]string{
					"web-bot-auth",
					"--expect",
					test.expectation,
				},
				&stdout,
				&stderr,
				environment.get,
				client,
			)

			if code != exitFailure {
				t.Fatalf(
					"code = %d, want failure",
					code,
				)
			}

			if !strings.Contains(
				stderr.String(),
				test.want,
			) {
				t.Fatalf(
					"stderr = %q, want %q",
					stderr.String(),
					test.want,
				)
			}
		})
	}

	t.Run("probe network failure", func(t *testing.T) {
		client := &conformanceClient{
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
			probeErr: errors.New(
				"integration probe failure",
			),
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

		if code != exitFailure ||
			!strings.Contains(
				stderr.String(),
				"integration probe failure",
			) {
			t.Fatalf(
				"code = %d stderr = %q",
				code,
				stderr.String(),
			)
		}
	})
}

func TestConformanceIntegrationDefensiveBoundaries(
	t *testing.T,
) {
	t.Run("nil signer identity", func(t *testing.T) {
		_, err := webBotAuthConformanceSigner(nil)
		if !errors.Is(
			err,
			errWebBotAuthConformance,
		) {
			t.Fatalf(
				"error = %v, want conformance error",
				err,
			)
		}
	})

	t.Run("nil directory authority", func(t *testing.T) {
		_, err := webBotAuthDirectoryAuthority(nil)
		if !errors.Is(
			err,
			errWebBotAuthConformance,
		) {
			t.Fatalf(
				"error = %v, want conformance error",
				err,
			)
		}
	})

	t.Run("nil fetch client", func(t *testing.T) {
		_, err := fetchWebBotAuth(
			context.Background(),
			nil,
			&url.URL{
				Scheme: "https",
				Host:   "example.com",
			},
			1,
		)
		if !errors.Is(
			err,
			errWebBotAuthConformance,
		) {
			t.Fatalf(
				"error = %v, want conformance error",
				err,
			)
		}
	})

	t.Run("nil fetch target", func(t *testing.T) {
		_, err := fetchWebBotAuth(
			context.Background(),
			&conformanceClient{},
			nil,
			1,
		)
		if !errors.Is(
			err,
			errWebBotAuthConformance,
		) {
			t.Fatalf(
				"error = %v, want conformance error",
				err,
			)
		}
	})
}
