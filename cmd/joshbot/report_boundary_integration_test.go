package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/reporting"
)

var errReportBoundaryIntegrationFailure = errors.New(
	"integration report runtime failure",
)

type reportBoundaryIntegrationDatabase struct{}

func (*reportBoundaryIntegrationDatabase) Close() {}

func (*reportBoundaryIntegrationDatabase) Ping(context.Context) error {
	return nil
}

func (*reportBoundaryIntegrationDatabase) Pool() *pgxpool.Pool {
	return nil
}

type reportBoundaryIntegrationListener struct {
	closed bool
}

func (*reportBoundaryIntegrationListener) Accept() (net.Conn, error) {
	return nil, errReportBoundaryIntegrationFailure
}

func (listener *reportBoundaryIntegrationListener) Close() error {
	listener.closed = true
	return nil
}

func (*reportBoundaryIntegrationListener) Addr() net.Addr {
	return reportBoundaryIntegrationAddress("127.0.0.1:8788")
}

type reportBoundaryIntegrationAddress string

func (reportBoundaryIntegrationAddress) Network() string {
	return "tcp"
}

func (address reportBoundaryIntegrationAddress) String() string {
	return string(address)
}

type reportBoundaryIntegrationServer struct {
	serveResult         chan error
	shutdownErr         error
	shutdownServeResult error
	serveCalls          int
	shutdownCalls       int
}

func (server *reportBoundaryIntegrationServer) Serve(net.Listener) error {
	server.serveCalls++
	return <-server.serveResult
}

func (server *reportBoundaryIntegrationServer) Shutdown(context.Context) error {
	server.shutdownCalls++

	if server.shutdownErr != nil {
		return server.shutdownErr
	}

	server.serveResult <- server.shutdownServeResult

	return nil
}

func TestReportBoundaryIntegrationConstructors(t *testing.T) {
	if _, err := newReportReader(
		nil,
		reporting.PostgresConfig{
			MaxPendingProbes: 1,
		},
	); err == nil {
		t.Error("newReportReader(nil) error = nil, want error")
	}
}

func TestReportBoundaryIntegrationRunReportFailures(t *testing.T) {
	var nilContext context.Context
	if err := runReport(
		nilContext,
		reportBoundaryIntegrationDependencies(),
	); !errors.Is(err, errInvalidReportConfiguration) {
		t.Errorf(
			"runReport(nil) error = %v, want %v",
			err,
			errInvalidReportConfiguration,
		)
	}

	if err := runReport(
		context.Background(),
		reportRuntimeDependencies{},
	); !errors.Is(err, errReportRuntimeUnavailable) {
		t.Errorf(
			"runReport(missing dependencies) error = %v, want %v",
			err,
			errReportRuntimeUnavailable,
		)
	}

	tests := []struct {
		name   string
		change func(*reportRuntimeDependencies)
		want   error
	}{
		{
			name: "settings",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.getenv = func(string) string {
					return ""
				}
			},
			want: errInvalidReportConfiguration,
		},
		{
			name: "token",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.readToken = func(
					string,
				) (string, error) {
					return "",
						errReportBoundaryIntegrationFailure
				}
			},
			want: errReportBoundaryIntegrationFailure,
		},
		{
			name: "database wrapper",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.withDatabase = func(
					context.Context,
					func(databaseConnection) error,
				) error {
					return errReportBoundaryIntegrationFailure
				}
			},
			want: errReportBoundaryIntegrationFailure,
		},
		{
			name: "nil database",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.withDatabase = func(
					_ context.Context,
					action func(databaseConnection) error,
				) error {
					return action(nil)
				}
			},
			want: errReportDatabaseUnavailable,
		},
		{
			name: "reader",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newReader = func(
					*pgxpool.Pool,
					reporting.PostgresConfig,
				) (reporting.Reader, error) {
					return nil,
						errReportBoundaryIntegrationFailure
				}
			},
			want: errReportBoundaryIntegrationFailure,
		},
		{
			name: "handler",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newHandler = func(
					reporting.Reader,
					string,
				) (http.Handler, error) {
					return nil,
						errReportBoundaryIntegrationFailure
				}
			},
			want: errReportBoundaryIntegrationFailure,
		},
		{
			name: "listener",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.listen = func(
					string,
					string,
				) (net.Listener, error) {
					return nil,
						errReportBoundaryIntegrationFailure
				}
			},
			want: errReportBoundaryIntegrationFailure,
		},
		{
			name: "server",
			change: func(
				dependencies *reportRuntimeDependencies,
			) {
				dependencies.newServer = func(
					reportSettings,
					http.Handler,
				) reportHTTPServer {
					return nil
				}
			},
			want: errReportServerUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies :=
				reportBoundaryIntegrationDependencies()

			test.change(&dependencies)

			err := runReport(
				context.Background(),
				dependencies,
			)

			if !errors.Is(err, test.want) {
				t.Errorf(
					"runReport() error = %v, want %v",
					err,
					test.want,
				)
			}
		})
	}
}

func TestReportBoundaryIntegrationRunReportServesAndCloses(
	t *testing.T,
) {
	dependencies :=
		reportBoundaryIntegrationDependencies()

	listener := &reportBoundaryIntegrationListener{}

	server := &reportBoundaryIntegrationServer{
		serveResult: make(chan error, 1),
	}
	server.serveResult <- http.ErrServerClosed

	dependencies.listen = func(
		network string,
		address string,
	) (net.Listener, error) {
		if network != "tcp" ||
			address != defaultReportListenAddress {
			t.Errorf(
				"listen(%q, %q)",
				network,
				address,
			)
		}

		return listener, nil
	}

	dependencies.newServer = func(
		settings reportSettings,
		handler http.Handler,
	) reportHTTPServer {
		if settings.listenAddress !=
			defaultReportListenAddress ||
			handler == nil {
			t.Errorf(
				"newServer(%#v, %#v)",
				settings,
				handler,
			)
		}

		return server
	}

	if err := runReport(
		context.Background(),
		dependencies,
	); err != nil {
		t.Fatalf("runReport() error = %v", err)
	}

	if !listener.closed ||
		server.serveCalls != 1 {
		t.Errorf(
			"listener closed = %t, serve calls = %d, want true, 1",
			listener.closed,
			server.serveCalls,
		)
	}
}

func TestReportBoundaryIntegrationServeReport(t *testing.T) {
	listener := &reportBoundaryIntegrationListener{}

	var nilContext context.Context
	if err := serveReport(
		nilContext,
		nil,
		nil,
		0,
	); !errors.Is(
		err,
		errInvalidReportConfiguration,
	) {
		t.Errorf(
			"serveReport(invalid) error = %v, want %v",
			err,
			errInvalidReportConfiguration,
		)
	}

	for _, result := range []error{
		nil,
		http.ErrServerClosed,
		errReportBoundaryIntegrationFailure,
	} {
		t.Run(
			reportBoundaryIntegrationErrorName(result),
			func(t *testing.T) {
				server :=
					&reportBoundaryIntegrationServer{
						serveResult: make(
							chan error,
							1,
						),
					}

				server.serveResult <- result

				err := serveReport(
					context.Background(),
					server,
					listener,
					time.Second,
				)

				if result == nil ||
					errors.Is(
						result,
						http.ErrServerClosed,
					) {
					if err != nil {
						t.Errorf(
							"serveReport() error = %v, want nil",
							err,
						)
					}
				} else if !errors.Is(
					err,
					result,
				) {
					t.Errorf(
						"serveReport() error = %v, want %v",
						err,
						result,
					)
				}
			},
		)
	}

	shutdownTests := []struct {
		name        string
		shutdownErr error
		stopResult  error
		want        error
	}{
		{
			name:       "clean nil stop",
			stopResult: nil,
		},
		{
			name:       "clean server closed",
			stopResult: http.ErrServerClosed,
		},
		{
			name:        "shutdown failure",
			shutdownErr: errReportBoundaryIntegrationFailure,
			want:        errReportBoundaryIntegrationFailure,
		},
		{
			name:       "stop failure",
			stopResult: errReportBoundaryIntegrationFailure,
			want:       errReportBoundaryIntegrationFailure,
		},
	}

	for _, test := range shutdownTests {
		t.Run(test.name, func(t *testing.T) {
			server :=
				&reportBoundaryIntegrationServer{
					serveResult: make(
						chan error,
						1,
					),
					shutdownErr:         test.shutdownErr,
					shutdownServeResult: test.stopResult,
				}

			ctx, cancel := context.WithCancel(
				context.Background(),
			)
			cancel()

			err := serveReport(
				ctx,
				server,
				listener,
				time.Second,
			)

			if test.shutdownErr != nil {
				server.serveResult <- http.ErrServerClosed
			}

			if test.want == nil {
				if err != nil {
					t.Errorf(
						"serveReport() error = %v, want nil",
						err,
					)
				}
			} else if !errors.Is(
				err,
				test.want,
			) {
				t.Errorf(
					"serveReport() error = %v, want %v",
					err,
					test.want,
				)
			}

			if server.shutdownCalls != 1 {
				t.Errorf(
					"shutdown calls = %d, want 1",
					server.shutdownCalls,
				)
			}
		})
	}
}

func reportBoundaryIntegrationDependencies() reportRuntimeDependencies {
	return reportRuntimeDependencies{
		getenv: func(name string) string {
			switch name {
			case reportTokenFileEnvironment:
				return "/secret/report-token"

			case reportListenAddressEnvironment:
				return defaultReportListenAddress

			default:
				return ""
			}
		},

		withDatabase: func(
			_ context.Context,
			action func(databaseConnection) error,
		) error {
			return action(
				&reportBoundaryIntegrationDatabase{},
			)
		},

		readToken: func(
			string,
		) (string, error) {
			return "integration-report-token",
				nil
		},

		newReader: func(
			*pgxpool.Pool,
			reporting.PostgresConfig,
		) (reporting.Reader, error) {
			return nil, nil
		},

		newHandler: func(
			reporting.Reader,
			string,
		) (http.Handler, error) {
			return http.HandlerFunc(
				func(
					http.ResponseWriter,
					*http.Request,
				) {
				},
			), nil
		},

		listen: func(
			string,
			string,
		) (net.Listener, error) {
			return &reportBoundaryIntegrationListener{},
				nil
		},

		newServer: func(
			reportSettings,
			http.Handler,
		) reportHTTPServer {
			server :=
				&reportBoundaryIntegrationServer{
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

func reportBoundaryIntegrationErrorName(
	err error,
) string {
	switch {
	case err == nil:
		return "nil"

	case errors.Is(
		err,
		http.ErrServerClosed,
	):
		return "server closed"

	default:
		return "failure"
	}
}
