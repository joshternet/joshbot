package robots

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCheckerSignsScheduledRobotsAndProtectedRequests(
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

	start := time.Date(
		2026,
		time.September,
		20,
		0,
		0,
		0,
		0,
		time.UTC,
	)
	clock := &requestDelayTestClock{
		current: start,
	}
	waiter := &requestDelayRecordingWaiter{
		clock: clock,
	}

	observations := make(
		[]webBotAuthIntegrationObservation,
		0,
		7,
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

			switch request.Host + request.URL.Path {
			case "source.example/robots.txt":
				writer.Header().Set(
					"Location",
					"/robots-v2.txt",
				)
				writer.WriteHeader(http.StatusFound)

			case "source.example/robots-v2.txt":
				writer.Header().Set(
					"Location",
					"http://policy.example/robots.txt",
				)
				writer.WriteHeader(http.StatusFound)

			case "policy.example/robots.txt":
				_, _ = writer.Write([]byte(
					"User-agent: " + ProductToken + "\n" +
						"Crawl-delay: 3\n" +
						"Allow: /\n",
				))

			case "source.example/start":
				writer.Header().Set(
					"Location",
					"/land",
				)
				writer.WriteHeader(http.StatusFound)

			case "source.example/land":
				_, _ = writer.Write([]byte("land"))

			case "other.example/robots.txt":
				_, _ = writer.Write([]byte(
					"User-agent: " + ProductToken + "\n" +
						"Crawl-delay: 1\n" +
						"Allow: /\n",
				))

			case "other.example/page":
				_, _ = writer.Write([]byte("page"))

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

	recording := &webBotAuthScheduledObservation{
		clock: clock,
		inner: &guardedHTTP{
			resolver: webBotAuthIntegrationResolver{},
			dialer: webBotAuthIntegrationDialer{
				target: server.Listener.Addr().String(),
			},
			signer: signer,
		},
	}

	checker := newCheckerWithRequestDelay(
		recording,
		clock.Now,
		2*time.Second,
		waiter,
	)

	readCheckedBody := func(
		target string,
		wantBody string,
	) {
		t.Helper()

		response, err := checker.Get(
			context.Background(),
			requestDelayTarget(t, target),
		)
		if err != nil {
			t.Fatalf(
				"Checker.Get(%q) error = %v, want nil",
				target,
				err,
			)
		}

		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read response body: %v", err)
		}

		if err := response.Body.Close(); err != nil {
			t.Fatalf("close response body: %v", err)
		}

		if string(body) != wantBody {
			t.Fatalf(
				"Checker.Get(%q) body = %q, want %q",
				target,
				body,
				wantBody,
			)
		}
	}

	startResponse, err := checker.Get(
		context.Background(),
		requestDelayTarget(
			t,
			"http://source.example/start",
		),
	)
	if err != nil {
		t.Fatalf(
			"Checker.Get(start) error = %v, want nil",
			err,
		)
	}

	if startResponse.StatusCode != http.StatusFound {
		t.Fatalf(
			"start status = %d, want %d",
			startResponse.StatusCode,
			http.StatusFound,
		)
	}

	if err := startResponse.Body.Close(); err != nil {
		t.Fatalf("close start body: %v", err)
	}

	readCheckedBody(
		"http://source.example/land",
		"land",
	)
	readCheckedBody(
		"http://other.example/page",
		"page",
	)

	wantTargets := []string{
		"http://source.example/robots.txt",
		"http://source.example/robots-v2.txt",
		"http://policy.example/robots.txt",
		"http://source.example/start",
		"http://source.example/land",
		"http://other.example/robots.txt",
		"http://other.example/page",
	}
	if got := recording.targets; !slices.Equal(
		got,
		wantTargets,
	) {
		t.Fatalf(
			"HTTP targets = %v, want %v",
			got,
			wantTargets,
		)
	}

	assertRequestDelayOffsets(
		t,
		start,
		recording.requests,
		[]time.Duration{
			0,
			2 * time.Second,
			2 * time.Second,
			5 * time.Second,
			8 * time.Second,
			8 * time.Second,
			10 * time.Second,
		},
	)

	if want := []time.Duration{
		2 * time.Second,
		3 * time.Second,
		3 * time.Second,
		2 * time.Second,
	}; !slices.Equal(waiter.durations, want) {
		t.Errorf(
			"wait durations = %v, want %v",
			waiter.durations,
			want,
		)
	}

	if len(observations) != len(wantTargets) {
		t.Fatalf(
			"signed request count = %d, want %d",
			len(observations),
			len(wantTargets),
		)
	}

	nonces := make(map[string]struct{}, len(observations))
	signatureInputs := make(
		map[string]struct{},
		len(observations),
	)

	for index, observation := range observations {
		want, err := url.Parse(wantTargets[index])
		if err != nil {
			t.Fatalf("url.Parse() error = %v", err)
		}

		if observation.authority != want.Host ||
			observation.path != want.Path {
			t.Errorf(
				"request %d = %s%s, want %s%s",
				index,
				observation.authority,
				observation.path,
				want.Host,
				want.Path,
			)
		}

		verifyWebBotAuthIntegrationSignature(
			t,
			identity,
			observation,
		)

		nonce, ok := quotedWebBotAuthParameter(
			strings.TrimPrefix(
				observation.signatureInput,
				"sig1=",
			),
			"nonce",
		)
		if !ok {
			t.Fatalf(
				"request %d nonce missing",
				index,
			)
		}

		nonces[nonce] = struct{}{}
		signatureInputs[observation.signatureInput] =
			struct{}{}
	}

	if len(nonces) != len(observations) ||
		len(signatureInputs) != len(observations) {
		t.Fatalf(
			"unique nonces = %d, unique signatures = %d, want %d",
			len(nonces),
			len(signatureInputs),
			len(observations),
		)
	}
}

type webBotAuthScheduledObservation struct {
	clock    *requestDelayTestClock
	inner    hopGetter
	requests []requestDelayRecordedRequest
	targets  []string
}

func (observation *webBotAuthScheduledObservation) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	observation.requests = append(
		observation.requests,
		requestDelayRecordedRequest{
			target: target.String(),
			at:     observation.clock.Now(),
		},
	)
	observation.targets = append(
		observation.targets,
		target.String(),
	)

	return observation.inner.get(ctx, target)
}
