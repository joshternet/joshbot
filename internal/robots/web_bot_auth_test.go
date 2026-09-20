package robots

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"
)

func TestCheckerSignsRobotsRedirectsAndProtectedRequests(
	t *testing.T,
) {
	serverObservations := make(
		[]signedRequestObservation,
		0,
		3,
	)

	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			serverObservations = append(
				serverObservations,
				signedRequestObservation{
					authority: request.Host,
					path:      request.URL.Path,
					userAgent: request.UserAgent(),
					signature: request.Header.Get(
						"X-JoshBot-Test-Signature",
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

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{
		dial: func(
			ctx context.Context,
			network string,
			_ string,
		) (net.Conn, error) {
			var local net.Dialer

			return local.DialContext(
				ctx,
				network,
				server.Listener.Addr().String(),
			)
		},
	}
	signer := &recordingRequestSigner{}

	checker := NewCheckerWithRequestDelayAndSigner(
		resolver,
		dialer,
		0,
		signer,
	)

	response, err := checker.Get(
		context.Background(),
		mustTarget(
			t,
			"http://example.com/page",
		),
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
		t.Errorf(
			"response body = %q, want %q",
			got,
			"page",
		)
	}

	wantSignerObservations := []signerObservation{
		{
			authority: "example.com",
			path:      "/robots.txt",
			userAgent: UserAgent,
		},
		{
			authority: "policy.example",
			path:      "/robots.txt",
			userAgent: UserAgent,
		},
		{
			authority: "example.com",
			path:      "/page",
			userAgent: UserAgent,
		},
	}

	if !slices.Equal(
		signer.observations,
		wantSignerObservations,
	) {
		t.Errorf(
			"signer observations = %#v, want %#v",
			signer.observations,
			wantSignerObservations,
		)
	}

	wantServerObservations := []signedRequestObservation{
		{
			authority: "example.com",
			path:      "/robots.txt",
			userAgent: UserAgent,
			signature: "example.com#1",
		},
		{
			authority: "policy.example",
			path:      "/robots.txt",
			userAgent: UserAgent,
			signature: "policy.example#2",
		},
		{
			authority: "example.com",
			path:      "/page",
			userAgent: UserAgent,
			signature: "example.com#3",
		},
	}

	if !slices.Equal(
		serverObservations,
		wantServerObservations,
	) {
		t.Errorf(
			"server observations = %#v, want %#v",
			serverObservations,
			wantServerObservations,
		)
	}
}

func TestGuardedHTTPStopsBeforeDialWhenSigningFails(
	t *testing.T,
) {
	signFailure := errors.New(
		"test signing failure",
	)

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{}
	signer := &recordingRequestSigner{
		err: signFailure,
	}

	getter := &guardedHTTP{
		resolver: resolver,
		dialer:   dialer,
		signer:   signer,
	}

	response, err := getter.get(
		context.Background(),
		mustTarget(
			t,
			"http://example.com/robots.txt",
		),
	)

	if response != nil {
		_ = response.Body.Close()

		t.Fatal(
			"guardedHTTP.get() response is non-nil after signing failure",
		)
	}

	if !errors.Is(err, signFailure) {
		t.Fatalf(
			"guardedHTTP.get() error = %v, want signing failure",
			err,
		)
	}

	if addresses := dialer.addressSnapshot(); len(addresses) != 0 {
		t.Errorf(
			"dial addresses = %v, want none",
			addresses,
		)
	}

	wantSignerObservations := []signerObservation{
		{
			authority: "example.com",
			path:      "/robots.txt",
			userAgent: UserAgent,
		},
	}

	if !slices.Equal(
		signer.observations,
		wantSignerObservations,
	) {
		t.Errorf(
			"signer observations = %#v, want %#v",
			signer.observations,
			wantSignerObservations,
		)
	}
}

type recordingRequestSigner struct {
	observations []signerObservation
	err          error
}

func (signer *recordingRequestSigner) Sign(
	request *http.Request,
) error {
	signer.observations = append(
		signer.observations,
		signerObservation{
			authority: request.URL.Host,
			path:      request.URL.Path,
			userAgent: request.UserAgent(),
		},
	)

	if signer.err != nil {
		return signer.err
	}

	request.Header.Set(
		"X-JoshBot-Test-Signature",
		request.URL.Host+"#"+
			string(rune('0'+len(signer.observations))),
	)

	return nil
}

type signerObservation struct {
	authority string
	path      string
	userAgent string
}

type signedRequestObservation struct {
	authority string
	path      string
	userAgent string
	signature string
}
