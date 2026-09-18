package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/reporting"
)

type reportTestDatabase struct{}

func (*reportTestDatabase) Close() {}

func (*reportTestDatabase) Ping(
	context.Context,
) error {
	return nil
}

func (*reportTestDatabase) Pool() *pgxpool.Pool {
	return nil
}

type reportTestListener struct {
	closed bool
}

func (*reportTestListener) Accept() (
	net.Conn,
	error,
) {
	return nil, errors.New(
		"test listener does not accept",
	)
}

func (listener *reportTestListener) Close() error {
	listener.closed = true

	return nil
}

func (*reportTestListener) Addr() net.Addr {
	return reportTestAddress(
		"127.0.0.1:8788",
	)
}

type reportTestAddress string

func (reportTestAddress) Network() string {
	return "tcp"
}

func (address reportTestAddress) String() string {
	return string(address)
}

type reportTestServer struct {
	serveResult chan error

	shutdownErr         error
	shutdownServeResult error

	serveCalls    int
	shutdownCalls int
}

func (server *reportTestServer) Serve(
	net.Listener,
) error {
	server.serveCalls++

	return <-server.serveResult
}

func (server *reportTestServer) Shutdown(
	context.Context,
) error {
	server.shutdownCalls++

	if server.shutdownErr != nil {
		return server.shutdownErr
	}

	server.serveResult <- server.shutdownServeResult

	return nil
}

type reportTestHandler struct{}

func (*reportTestHandler) ServeHTTP(
	http.ResponseWriter,
	*http.Request,
) {
}

type failingReportReader struct{}

func (failingReportReader) Read(
	[]byte,
) (int, error) {
	return 0, errors.New(
		"test read failure",
	)
}

func TestRuntimeOperationsReportUsesRuntime(
	t *testing.T,
) {
	operations := runtimeOperations{}

	err := operations.report(
		context.Background(),
	)

	if !errors.Is(
		err,
		errReportRuntimeUnavailable,
	) {
		t.Errorf(
			"report() error = %v, want %v",
			err,
			errReportRuntimeUnavailable,
		)
	}
}

func TestLoadReportSettings(
	t *testing.T,
) {
	validEnvironment := map[string]string{
		reportTokenFileEnvironment:                "/run/secrets/joshbot_report_token",
		automaticCrawlEnabledEnvironment:          "true",
		automaticCrawlMaxPendingProbesEnvironment: "250",
	}

	settings, err := loadReportSettings(
		reportEnvironment(validEnvironment),
	)
	if err != nil {
		t.Fatalf(
			"loadReportSettings() error = %v",
			err,
		)
	}

	if settings.listenAddress !=
		defaultReportListenAddress {
		t.Errorf(
			"listen address = %q, want %q",
			settings.listenAddress,
			defaultReportListenAddress,
		)
	}

	if settings.tokenFile !=
		"/run/secrets/joshbot_report_token" {
		t.Errorf(
			"token file = %q",
			settings.tokenFile,
		)
	}

	if !settings.automaticCrawlEnabled {
		t.Error(
			"automatic crawl enabled = false, want true",
		)
	}

	if settings.maxPendingProbes != 250 {
		t.Errorf(
			"max pending probes = %d, want 250",
			settings.maxPendingProbes,
		)
	}

	if settings.readHeaderTimeout !=
		defaultReportReadHeaderTimeout ||
		settings.readTimeout !=
			defaultReportReadTimeout ||
		settings.writeTimeout !=
			defaultReportWriteTimeout ||
		settings.idleTimeout !=
			defaultReportIdleTimeout ||
		settings.shutdownTimeout !=
			defaultReportShutdownTimeout {
		t.Errorf(
			"report timeouts = %#v",
			settings,
		)
	}
}

func TestLoadReportSettingsUsesConfiguredAddress(
	t *testing.T,
) {
	settings, err := loadReportSettings(
		reportEnvironment(
			map[string]string{
				reportTokenFileEnvironment:     "/secret/token",
				reportListenAddressEnvironment: "0.0.0.0:9000",
			},
		),
	)
	if err != nil {
		t.Fatalf(
			"loadReportSettings() error = %v",
			err,
		)
	}

	if settings.listenAddress !=
		"0.0.0.0:9000" {
		t.Errorf(
			"listen address = %q",
			settings.listenAddress,
		)
	}

	if settings.automaticCrawlEnabled {
		t.Error(
			"automatic crawl enabled = true, want false",
		)
	}

	if settings.maxPendingProbes !=
		defaultAutomaticCrawlMaxPendingProbes {
		t.Errorf(
			"max pending probes = %d, want %d",
			settings.maxPendingProbes,
			defaultAutomaticCrawlMaxPendingProbes,
		)
	}
}

func TestLoadReportSettingsRejectsInvalidConfiguration(
	t *testing.T,
) {
	tests := []struct {
		name        string
		environment map[string]string
	}{
		{
			name:        "missing token file",
			environment: map[string]string{},
		},
		{
			name: "blank token file",
			environment: map[string]string{
				reportTokenFileEnvironment: "   ",
			},
		},
		{
			name: "invalid listen address",
			environment: map[string]string{
				reportTokenFileEnvironment:     "/secret/token",
				reportListenAddressEnvironment: "localhost",
			},
		},
		{
			name: "invalid port",
			environment: map[string]string{
				reportTokenFileEnvironment:     "/secret/token",
				reportListenAddressEnvironment: "localhost:nope",
			},
		},
		{
			name: "zero port",
			environment: map[string]string{
				reportTokenFileEnvironment:     "/secret/token",
				reportListenAddressEnvironment: "localhost:0",
			},
		},
		{
			name: "port too large",
			environment: map[string]string{
				reportTokenFileEnvironment:     "/secret/token",
				reportListenAddressEnvironment: "localhost:65536",
			},
		},
		{
			name: "invalid automatic boolean",
			environment: map[string]string{
				reportTokenFileEnvironment:       "/secret/token",
				automaticCrawlEnabledEnvironment: "not-a-bool",
			},
		},
		{
			name: "invalid automatic limit",
			environment: map[string]string{
				reportTokenFileEnvironment:                "/secret/token",
				automaticCrawlEnabledEnvironment:          "true",
				automaticCrawlMaxPendingProbesEnvironment: "0",
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				_, err := loadReportSettings(
					reportEnvironment(
						test.environment,
					),
				)

				if err == nil {
					t.Fatal(
						"error = nil, want configuration failure",
					)
				}
			},
		)
	}

	_, err := loadReportSettings(nil)

	if !errors.Is(
		err,
		errInvalidReportConfiguration,
	) {
		t.Errorf(
			"nil getenv error = %v, want %v",
			err,
			errInvalidReportConfiguration,
		)
	}
}

func TestLoadReportSettingsIgnoresAutomaticLimitWhenDisabled(
	t *testing.T,
) {
	settings, err := loadReportSettings(
		reportEnvironment(
			map[string]string{
				reportTokenFileEnvironment:                "/secret/token",
				automaticCrawlEnabledEnvironment:          "false",
				automaticCrawlMaxPendingProbesEnvironment: "invalid",
			},
		),
	)
	if err != nil {
		t.Fatalf(
			"loadReportSettings() error = %v",
			err,
		)
	}

	if settings.maxPendingProbes !=
		defaultAutomaticCrawlMaxPendingProbes {
		t.Errorf(
			"max pending probes = %d, want %d",
			settings.maxPendingProbes,
			defaultAutomaticCrawlMaxPendingProbes,
		)
	}
}

func TestReadReportToken(
	t *testing.T,
) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain",
			raw:  "secret-token",
			want: "secret-token",
		},
		{
			name: "newline",
			raw:  "secret-token\n",
			want: "secret-token",
		},
		{
			name: "windows newline",
			raw:  "secret-token\r\n",
			want: "secret-token",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				got, err := readReportToken(
					strings.NewReader(
						test.raw,
					),
				)
				if err != nil {
					t.Fatalf(
						"readReportToken() error = %v",
						err,
					)
				}

				if got != test.want {
					t.Errorf(
						"token = %q, want %q",
						got,
						test.want,
					)
				}
			},
		)
	}
}

func TestReadReportTokenRejectsInvalidInput(
	t *testing.T,
) {
	if _, err := readReportToken(
		nil,
	); !errors.Is(
		err,
		errReportTokenReaderUnavailable,
	) {
		t.Errorf(
			"nil reader error = %v",
			err,
		)
	}

	if _, err := readReportToken(
		strings.NewReader(""),
	); !errors.Is(
		err,
		errEmptyReportToken,
	) {
		t.Errorf(
			"empty token error = %v",
			err,
		)
	}

	if _, err := readReportToken(
		strings.NewReader(
			strings.Repeat(
				"x",
				maxReportTokenSize+1,
			),
		),
	); !errors.Is(
		err,
		errReportTokenTooLarge,
	) {
		t.Errorf(
			"large token error = %v",
			err,
		)
	}

	if _, err := readReportToken(
		strings.NewReader(
			"first\nsecond",
		),
	); !errors.Is(
		err,
		errInvalidReportToken,
	) {
		t.Errorf(
			"multiline token error = %v",
			err,
		)
	}

	if _, err := readReportToken(
		failingReportReader{},
	); !errors.Is(
		err,
		errReadReportTokenFile,
	) {
		t.Errorf(
			"read failure = %v",
			err,
		)
	}
}

func TestReadReportTokenFile(
	t *testing.T,
) {
	tokenFile := t.TempDir() +
		"/report-token"

	if err := os.WriteFile(
		tokenFile,
		[]byte("test-token\n"),
		0600,
	); err != nil {
		t.Fatalf(
			"write token file: %v",
			err,
		)
	}

	token, err := readReportTokenFile(
		tokenFile,
	)
	if err != nil {
		t.Fatalf(
			"readReportTokenFile() error = %v",
			err,
		)
	}

	if token != "test-token" {
		t.Errorf(
			"token = %q, want test-token",
			token,
		)
	}

	if _, err := readReportTokenFile(
		tokenFile + "-missing",
	); !errors.Is(
		err,
		errOpenReportTokenFile,
	) {
		t.Errorf(
			"missing token error = %v",
			err,
		)
	}
}

func TestReportConstructors(
	t *testing.T,
) {
	if _, err := newReportReader(
		nil,
		reporting.PostgresConfig{
			MaxPendingProbes: 1,
		},
	); err == nil {
		t.Error(
			"newReportReader(nil) error = nil",
		)
	}

	if _, err := newReportHandler(
		nil,
		"token",
	); err == nil {
		t.Error(
			"newReportHandler(nil) error = nil",
		)
	}

	handler := &reportTestHandler{}
	settings := reportTestSettings()

	server := newReportHTTPServer(
		settings,
		handler,
	)

	httpServer, ok := server.(*http.Server)
	if !ok {
		t.Fatalf(
			"server type = %T, want *http.Server",
			server,
		)
	}

	if httpServer.Addr !=
		settings.listenAddress {
		t.Errorf(
			"server address = %q, want %q",
			httpServer.Addr,
			settings.listenAddress,
		)
	}

	if httpServer.Handler != handler {
		t.Error(
			"server handler does not match configured handler",
		)
	}

	if httpServer.ReadHeaderTimeout !=
		settings.readHeaderTimeout {
		t.Errorf(
			"read header timeout = %v, want %v",
			httpServer.ReadHeaderTimeout,
			settings.readHeaderTimeout,
		)
	}

	if httpServer.ReadTimeout !=
		settings.readTimeout {
		t.Errorf(
			"read timeout = %v, want %v",
			httpServer.ReadTimeout,
			settings.readTimeout,
		)
	}

	if httpServer.WriteTimeout !=
		settings.writeTimeout {
		t.Errorf(
			"write timeout = %v, want %v",
			httpServer.WriteTimeout,
			settings.writeTimeout,
		)
	}

	if httpServer.IdleTimeout !=
		settings.idleTimeout {
		t.Errorf(
			"idle timeout = %v, want %v",
			httpServer.IdleTimeout,
			settings.idleTimeout,
		)
	}
}

func TestRunReportRejectsInvalidRuntime(
	t *testing.T,
) {
	dependencies := validReportDependencies(
		t,
	)

	if err := runReport(
		nil,
		dependencies,
	); !errors.Is(
		err,
		errInvalidReportConfiguration,
	) {
		t.Errorf(
			"nil context error = %v",
			err,
		)
	}

	dependencies.getenv = nil

	if err := runReport(
		context.Background(),
		dependencies,
	); !errors.Is(
		err,
		errReportRuntimeUnavailable,
	) {
		t.Errorf(
			"invalid dependencies error = %v",
			err,
		)
	}
}

func TestRunReportPreservesConstructionFailures(
	t *testing.T,
) {
	expected := errors.New(
		"test failure",
	)

	tests := []struct {
		name string

		change func(
			*reportRuntimeDependencies,
		)
	}{
		{
			name: "token",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.readToken =
					func(string) (string, error) {
						return "", expected
					}
			},
		},
		{
			name: "database",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.withDatabase =
					func(
						context.Context,
						func(databaseConnection) error,
					) error {
						return expected
					}
			},
		},
		{
			name: "nil database",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.withDatabase =
					func(
						_ context.Context,
						action func(
							databaseConnection,
						) error,
					) error {
						return action(nil)
					}
			},
		},
		{
			name: "reader",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newReader =
					func(
						*pgxpool.Pool,
						reporting.PostgresConfig,
					) (
						reporting.Reader,
						error,
					) {
						return nil, expected
					}
			},
		},
		{
			name: "handler",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newHandler =
					func(
						reporting.Reader,
						string,
					) (
						http.Handler,
						error,
					) {
						return nil, expected
					}
			},
		},
		{
			name: "listener",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.listen =
					func(
						string,
						string,
					) (
						net.Listener,
						error,
					) {
						return nil, expected
					}
			},
		},
		{
			name: "server",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newServer =
					func(
						reportSettings,
						http.Handler,
					) reportHTTPServer {
						return nil
					}
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				dependencies :=
					validReportDependencies(t)

				test.change(&dependencies)

				err := runReport(
					context.Background(),
					dependencies,
				)

				if err == nil {
					t.Fatal(
						"error = nil, want failure",
					)
				}
			},
		)
	}
}

func TestRunReportServesAndClosesListener(
	t *testing.T,
) {
	dependencies := validReportDependencies(
		t,
	)

	listener := &reportTestListener{}

	server := &reportTestServer{
		serveResult: make(
			chan error,
			1,
		),
	}

	server.serveResult <- http.ErrServerClosed

	dependencies.listen =
		func(
			string,
			string,
		) (
			net.Listener,
			error,
		) {
			return listener, nil
		}

	dependencies.newServer =
		func(
			reportSettings,
			http.Handler,
		) reportHTTPServer {
			return server
		}

	err := runReport(
		context.Background(),
		dependencies,
	)
	if err != nil {
		t.Fatalf(
			"runReport() error = %v",
			err,
		)
	}

	if server.serveCalls != 1 {
		t.Errorf(
			"Serve() calls = %d, want 1",
			server.serveCalls,
		)
	}

	if !listener.closed {
		t.Error(
			"listener was not closed",
		)
	}
}

func TestServeReport(
	t *testing.T,
) {
	listener := &reportTestListener{}

	if err := serveReport(
		nil,
		nil,
		nil,
		0,
	); !errors.Is(
		err,
		errInvalidReportConfiguration,
	) {
		t.Errorf(
			"invalid serve error = %v",
			err,
		)
	}

	t.Run(
		"server error",
		func(t *testing.T) {
			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
			}

			server.serveResult <- errors.New("serve failed")

			err := serveReport(
				context.Background(),
				server,
				listener,
				time.Second,
			)

			if err == nil {
				t.Fatal(
					"error = nil, want serve failure",
				)
			}
		},
	)

	t.Run(
		"nil server result",
		func(t *testing.T) {
			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
			}

			server.serveResult <- nil

			err := serveReport(
				context.Background(),
				server,
				listener,
				time.Second,
			)

			if err != nil {
				t.Errorf(
					"error = %v, want nil",
					err,
				)
			}
		},
	)

	t.Run(
		"server closed result",
		func(t *testing.T) {
			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
			}

			server.serveResult <- http.ErrServerClosed

			err := serveReport(
				context.Background(),
				server,
				listener,
				time.Second,
			)

			if err != nil {
				t.Errorf(
					"error = %v, want nil",
					err,
				)
			}
		},
	)

	t.Run(
		"graceful cancellation",
		func(t *testing.T) {
			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
				shutdownServeResult: http.ErrServerClosed,
			}

			ctx, cancel :=
				context.WithCancel(
					context.Background(),
				)

			cancel()

			err := serveReport(
				ctx,
				server,
				listener,
				time.Second,
			)

			if err != nil {
				t.Errorf(
					"error = %v, want nil",
					err,
				)
			}

			if server.shutdownCalls != 1 {
				t.Errorf(
					"Shutdown() calls = %d, want 1",
					server.shutdownCalls,
				)
			}
		},
	)

	t.Run(
		"graceful cancellation nil result",
		func(t *testing.T) {
			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
				shutdownServeResult: nil,
			}

			ctx, cancel :=
				context.WithCancel(
					context.Background(),
				)

			cancel()

			err := serveReport(
				ctx,
				server,
				listener,
				time.Second,
			)

			if err != nil {
				t.Errorf(
					"error = %v, want nil",
					err,
				)
			}
		},
	)

	t.Run(
		"shutdown failure",
		func(t *testing.T) {
			expected := errors.New(
				"shutdown failed",
			)

			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
				shutdownErr: expected,
			}

			ctx, cancel :=
				context.WithCancel(
					context.Background(),
				)

			cancel()

			err := serveReport(
				ctx,
				server,
				listener,
				time.Second,
			)

			server.serveResult <- http.ErrServerClosed

			if !errors.Is(
				err,
				expected,
			) {
				t.Errorf(
					"error = %v, want %v",
					err,
					expected,
				)
			}
		},
	)

	t.Run(
		"unexpected stop result",
		func(t *testing.T) {
			expected := errors.New(
				"unexpected stop",
			)

			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
				shutdownServeResult: expected,
			}

			ctx, cancel :=
				context.WithCancel(
					context.Background(),
				)

			cancel()

			err := serveReport(
				ctx,
				server,
				listener,
				time.Second,
			)

			if !errors.Is(
				err,
				expected,
			) {
				t.Errorf(
					"error = %v, want %v",
					err,
					expected,
				)
			}
		},
	)
}

func TestValidReportRuntimeDependencies(
	t *testing.T,
) {
	valid := validReportDependencies(t)

	if !validReportRuntimeDependencies(
		valid,
	) {
		t.Fatal(
			"valid dependencies rejected",
		)
	}

	tests := []struct {
		name  string
		clear func(
			*reportRuntimeDependencies,
		)
	}{
		{
			name: "getenv",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.getenv = nil
			},
		},
		{
			name: "with database",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.withDatabase = nil
			},
		},
		{
			name: "read token",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.readToken = nil
			},
		},
		{
			name: "new reader",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newReader = nil
			},
		},
		{
			name: "new handler",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newHandler = nil
			},
		},
		{
			name: "listen",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.listen = nil
			},
		},
		{
			name: "new server",
			clear: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newServer = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				dependencies :=
					validReportDependencies(t)

				test.clear(&dependencies)

				if validReportRuntimeDependencies(
					dependencies,
				) {
					t.Error(
						"invalid dependencies accepted",
					)
				}
			},
		)
	}
}

func validReportDependencies(
	t *testing.T,
) reportRuntimeDependencies {
	t.Helper()

	settings := reportTestSettings()

	return reportRuntimeDependencies{
		getenv: func(
			name string,
		) string {
			switch name {
			case reportTokenFileEnvironment:
				return "/test/token"
			case reportListenAddressEnvironment:
				return settings.listenAddress
			default:
				return ""
			}
		},

		withDatabase: func(
			_ context.Context,
			action func(
				databaseConnection,
			) error,
		) error {
			return action(
				&reportTestDatabase{},
			)
		},

		readToken: func(
			string,
		) (string, error) {
			return "test-token", nil
		},

		newReader: func(
			*pgxpool.Pool,
			reporting.PostgresConfig,
		) (
			reporting.Reader,
			error,
		) {
			return nil, nil
		},

		newHandler: func(
			reporting.Reader,
			string,
		) (
			http.Handler,
			error,
		) {
			return &reportTestHandler{},
				nil
		},

		listen: func(
			string,
			string,
		) (
			net.Listener,
			error,
		) {
			return &reportTestListener{},
				nil
		},

		newServer: func(
			reportSettings,
			http.Handler,
		) reportHTTPServer {
			server := &reportTestServer{
				serveResult: make(
					chan error,
					1,
				),
			}

			server.serveResult <- http.ErrServerClosed

			return server
		},
	}
}

func reportEnvironment(
	values map[string]string,
) environmentGetter {
	return func(
		name string,
	) string {
		return values[name]
	}
}

func reportTestSettings() reportSettings {
	return reportSettings{
		listenAddress:         defaultReportListenAddress,
		tokenFile:             "/test/token",
		automaticCrawlEnabled: false,
		maxPendingProbes:      defaultAutomaticCrawlMaxPendingProbes,
		readHeaderTimeout:     defaultReportReadHeaderTimeout,
		readTimeout:           defaultReportReadTimeout,
		writeTimeout:          defaultReportWriteTimeout,
		idleTimeout:           defaultReportIdleTimeout,
		shutdownTimeout:       defaultReportShutdownTimeout,
	}
}

func TestRunReportPreservesSettingsFailure(
	t *testing.T,
) {
	dependencies := validReportDependencies(
		t,
	)

	dependencies.getenv = func(
		string,
	) string {
		return ""
	}

	err := runReport(
		context.Background(),
		dependencies,
	)

	if !errors.Is(
		err,
		errInvalidReportConfiguration,
	) {
		t.Errorf(
			"runReport() error = %v, want %v",
			err,
			errInvalidReportConfiguration,
		)
	}
}
