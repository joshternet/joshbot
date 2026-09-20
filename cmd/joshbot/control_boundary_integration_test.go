//lint:file-ignore SA1012 Intentional negative tests verify defensive nil-context rejection; production callers must never pass a nil context.
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
	"github.com/joshternet/joshbot/internal/control"
)

var (
	errControlBoundaryIntegrationFailure = errors.New(
		"integration control runtime failure",
	)
	errControlBoundaryIntegrationRead = errors.New(
		"integration control token read failure",
	)
)

type controlBoundaryIntegrationEnvironment map[string]string

func (environment controlBoundaryIntegrationEnvironment) get(name string) string {
	return environment[name]
}

type controlBoundaryIntegrationReader struct{}

func (controlBoundaryIntegrationReader) Read([]byte) (int, error) {
	return 0, errControlBoundaryIntegrationRead
}

type controlBoundaryIntegrationDatabase struct{}

func (*controlBoundaryIntegrationDatabase) Close() {}

func (*controlBoundaryIntegrationDatabase) Ping(context.Context) error {
	return nil
}

func (*controlBoundaryIntegrationDatabase) Pool() *pgxpool.Pool {
	return nil
}

type controlBoundaryIntegrationCommander struct{}

func (*controlBoundaryIntegrationCommander) SetProcessorPaused(
	context.Context,
	string,
	bool,
	control.Audit,
) error {
	return nil
}

func (*controlBoundaryIntegrationCommander) AddDomainAvoid(
	context.Context,
	string,
	control.Audit,
) error {
	return nil
}

func (*controlBoundaryIntegrationCommander) RemoveDomainAvoid(
	context.Context,
	string,
	control.Audit,
) error {
	return nil
}

func (*controlBoundaryIntegrationCommander) SetOriginBlocked(
	context.Context,
	string,
	bool,
	control.Audit,
) error {
	return nil
}

func (*controlBoundaryIntegrationCommander) RecordRejected(
	context.Context,
	control.Audit,
) error {
	return nil
}

type controlBoundaryIntegrationListener struct {
	closed bool
}

func (*controlBoundaryIntegrationListener) Accept() (net.Conn, error) {
	return nil, errControlBoundaryIntegrationFailure
}

func (listener *controlBoundaryIntegrationListener) Close() error {
	listener.closed = true
	return nil
}

func (*controlBoundaryIntegrationListener) Addr() net.Addr {
	return controlBoundaryIntegrationAddress("127.0.0.1:8789")
}

type controlBoundaryIntegrationAddress string

func (controlBoundaryIntegrationAddress) Network() string {
	return "tcp"
}

func (address controlBoundaryIntegrationAddress) String() string {
	return string(address)
}

type controlBoundaryIntegrationServer struct {
	serveResult         chan error
	shutdownErr         error
	shutdownServeResult error
	serveCalls          int
	shutdownCalls       int
}

func (server *controlBoundaryIntegrationServer) Serve(net.Listener) error {
	server.serveCalls++
	return <-server.serveResult
}

func (server *controlBoundaryIntegrationServer) Shutdown(context.Context) error {
	server.shutdownCalls++
	if server.shutdownErr != nil {
		return server.shutdownErr
	}

	server.serveResult <- server.shutdownServeResult
	return nil
}

func TestControlBoundaryIntegrationSettingsAndTokenFailures(t *testing.T) {
	if settings, err := loadControlSettings(nil); !errors.Is(
		err,
		errInvalidControlConfiguration,
	) || settings != (controlSettings{}) {
		t.Errorf("loadControlSettings(nil) = %#v, %v", settings, err)
	}

	for _, environment := range []controlBoundaryIntegrationEnvironment{
		{},
		{controlTokenFileEnvironment: "   "},
		{
			controlTokenFileEnvironment:     "/secret/token",
			controlListenAddressEnvironment: "localhost",
		},
		{
			controlTokenFileEnvironment:     "/secret/token",
			controlListenAddressEnvironment: "localhost:nope",
		},
		{
			controlTokenFileEnvironment:     "/secret/token",
			controlListenAddressEnvironment: "localhost:0",
		},
		{
			controlTokenFileEnvironment:     "/secret/token",
			controlListenAddressEnvironment: "localhost:65536",
		},
	} {
		settings, err := loadControlSettings(environment.get)
		if !errors.Is(err, errInvalidControlConfiguration) ||
			settings != (controlSettings{}) {
			t.Errorf(
				"loadControlSettings(%v) = %#v, %v, want zero settings and %v",
				environment,
				settings,
				err,
				errInvalidControlConfiguration,
			)
		}
	}

	validEnvironment := controlBoundaryIntegrationEnvironment{
		controlTokenFileEnvironment:     "/secret/operator-token",
		controlListenAddressEnvironment: "0.0.0.0:9001",
	}

	settings, err := loadControlSettings(validEnvironment.get)
	if err != nil {
		t.Fatalf("loadControlSettings(valid) error = %v", err)
	}

	if settings.listenAddress != "0.0.0.0:9001" ||
		settings.tokenFile != "/secret/operator-token" {
		t.Errorf("loadControlSettings(valid) = %#v", settings)
	}

	missing := t.TempDir() + "/missing-token"

	if token, err := readControlTokenFile(missing); !errors.Is(
		err,
		errOpenControlTokenFile,
	) || token != "" {
		t.Errorf("readControlTokenFile(missing) = %q, %v", token, err)
	}

	validToken := "0123456789abcdef0123456789abcdef"
	tokenFile := t.TempDir() + "/operator-token"

	if err := os.WriteFile(
		tokenFile,
		[]byte(validToken+"\r\n"),
		0o600,
	); err != nil {
		t.Fatalf("write control token: %v", err)
	}

	token, err := readControlTokenFile(tokenFile)
	if err != nil || token != validToken {
		t.Errorf(
			"readControlTokenFile(valid) = %q, %v",
			token,
			err,
		)
	}

	if token, err := readControlToken(nil); !errors.Is(
		err,
		errControlTokenReaderUnavailable,
	) || token != "" {
		t.Errorf("readControlToken(nil) = %q, %v", token, err)
	}

	if token, err := readControlToken(
		controlBoundaryIntegrationReader{},
	); !errors.Is(
		err,
		errReadControlTokenFile,
	) || token != "" {
		t.Errorf(
			"readControlToken(read failure) = %q, %v",
			token,
			err,
		)
	}

	invalidTokens := []string{
		"",
		"short",
		strings.Repeat("x", minControlTokenSize),
		strings.Repeat("x", maxControlTokenSize+1),
		" 123456789abcdef0123456789abcdef",
		"0123456789abcdef0123456789abcde ",
		"0123456789abcdef\n0123456789abcdef",
		"0123456789abcdef0123456789abcde\x7f",
		"0123456789abcdef0123456789abcdé",
	}

	for _, raw := range invalidTokens {
		if token, err := readControlToken(
			strings.NewReader(raw),
		); !errors.Is(
			err,
			errInvalidControlToken,
		) || token != "" {
			t.Errorf(
				"readControlToken(%q) = %q, %v",
				raw,
				token,
				err,
			)
		}
	}

	if _, err := newControlCommander(nil); err == nil {
		t.Error(
			"newControlCommander(nil) error = nil, want error",
		)
	}
}

func TestControlBoundaryIntegrationRunControlFailures(t *testing.T) {
	if err := runControl(
		nil,
		controlBoundaryIntegrationDependencies(),
	); !errors.Is(
		err,
		errInvalidControlConfiguration,
	) {
		t.Errorf(
			"runControl(nil) error = %v, want %v",
			err,
			errInvalidControlConfiguration,
		)
	}

	if err := runControl(
		context.Background(),
		controlRuntimeDependencies{},
	); !errors.Is(
		err,
		errControlRuntimeUnavailable,
	) {
		t.Errorf(
			"runControl(missing dependencies) error = %v, want %v",
			err,
			errControlRuntimeUnavailable,
		)
	}

	tests := []struct {
		name   string
		change func(*controlRuntimeDependencies)
		want   error
	}{
		{
			name: "settings",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.getenv =
					controlBoundaryIntegrationEnvironment{}.get
			},
			want: errInvalidControlConfiguration,
		},
		{
			name: "token",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.readToken = func(
					string,
				) (string, error) {
					return "",
						errControlBoundaryIntegrationFailure
				}
			},
			want: errControlBoundaryIntegrationFailure,
		},
		{
			name: "database wrapper",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.withDatabase = func(
					context.Context,
					func(databaseConnection) error,
				) error {
					return errControlBoundaryIntegrationFailure
				}
			},
			want: errControlBoundaryIntegrationFailure,
		},
		{
			name: "nil database",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.withDatabase = func(
					_ context.Context,
					action func(databaseConnection) error,
				) error {
					return action(nil)
				}
			},
			want: errControlDatabaseUnavailable,
		},
		{
			name: "commander",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.newCommander = func(
					*pgxpool.Pool,
				) (control.Commander, error) {
					return nil,
						errControlBoundaryIntegrationFailure
				}
			},
			want: errControlBoundaryIntegrationFailure,
		},
		{
			name: "handler",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.newHandler = func(
					control.Commander,
					string,
				) (http.Handler, error) {
					return nil,
						errControlBoundaryIntegrationFailure
				}
			},
			want: errControlBoundaryIntegrationFailure,
		},
		{
			name: "listener",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.listen = func(
					string,
					string,
				) (net.Listener, error) {
					return nil,
						errControlBoundaryIntegrationFailure
				}
			},
			want: errControlBoundaryIntegrationFailure,
		},
		{
			name: "server",
			change: func(
				dependencies *controlRuntimeDependencies,
			) {
				dependencies.newServer = func(
					controlSettings,
					http.Handler,
				) reportHTTPServer {
					return nil
				}
			},
			want: errControlServerUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies :=
				controlBoundaryIntegrationDependencies()

			test.change(&dependencies)

			err := runControl(
				context.Background(),
				dependencies,
			)

			if !errors.Is(err, test.want) {
				t.Errorf(
					"runControl() error = %v, want %v",
					err,
					test.want,
				)
			}
		})
	}
}

func TestControlBoundaryIntegrationRunControlServesAndCloses(
	t *testing.T,
) {
	dependencies :=
		controlBoundaryIntegrationDependencies()

	listener := &controlBoundaryIntegrationListener{}

	server := &controlBoundaryIntegrationServer{
		serveResult: make(chan error, 1),
	}
	server.serveResult <- http.ErrServerClosed

	dependencies.listen = func(
		network string,
		address string,
	) (net.Listener, error) {
		if network != "tcp" ||
			address != defaultControlListenAddress {
			t.Errorf(
				"listen(%q, %q)",
				network,
				address,
			)
		}

		return listener, nil
	}

	dependencies.newServer = func(
		settings controlSettings,
		handler http.Handler,
	) reportHTTPServer {
		if settings.listenAddress !=
			defaultControlListenAddress ||
			handler == nil {
			t.Errorf(
				"newServer(%#v, %#v)",
				settings,
				handler,
			)
		}

		return server
	}

	if err := runControl(
		context.Background(),
		dependencies,
	); err != nil {
		t.Fatalf("runControl() error = %v", err)
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

func TestControlBoundaryIntegrationServeControl(t *testing.T) {
	listener := &controlBoundaryIntegrationListener{}

	if err := serveControl(
		nil,
		nil,
		nil,
		0,
	); !errors.Is(
		err,
		errInvalidControlConfiguration,
	) {
		t.Errorf(
			"serveControl(invalid) error = %v, want %v",
			err,
			errInvalidControlConfiguration,
		)
	}

	for _, result := range []error{
		nil,
		http.ErrServerClosed,
		errControlBoundaryIntegrationFailure,
	} {
		t.Run(
			"immediate "+
				controlBoundaryIntegrationErrorName(result),
			func(t *testing.T) {
				server :=
					&controlBoundaryIntegrationServer{
						serveResult: make(
							chan error,
							1,
						),
					}

				server.serveResult <- result

				err := serveControl(
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
							"serveControl() error = %v, want nil",
							err,
						)
					}
				} else if !errors.Is(
					err,
					result,
				) {
					t.Errorf(
						"serveControl() error = %v, want %v",
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
			shutdownErr: errControlBoundaryIntegrationFailure,
			want:        errControlBoundaryIntegrationFailure,
		},
		{
			name:       "stop failure",
			stopResult: errControlBoundaryIntegrationFailure,
			want:       errControlBoundaryIntegrationFailure,
		},
	}

	for _, test := range shutdownTests {
		t.Run(test.name, func(t *testing.T) {
			server :=
				&controlBoundaryIntegrationServer{
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

			err := serveControl(
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
						"serveControl() error = %v, want nil",
						err,
					)
				}
			} else if !errors.Is(
				err,
				test.want,
			) {
				t.Errorf(
					"serveControl() error = %v, want %v",
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

func controlBoundaryIntegrationDependencies() controlRuntimeDependencies {
	return controlRuntimeDependencies{
		getenv: controlBoundaryIntegrationEnvironment{
			controlTokenFileEnvironment: "/secret/operator-token",
		}.get,

		withDatabase: func(
			_ context.Context,
			action func(databaseConnection) error,
		) error {
			return action(
				&controlBoundaryIntegrationDatabase{},
			)
		},

		readToken: func(
			string,
		) (string, error) {
			return "0123456789abcdef0123456789abcdef",
				nil
		},

		newCommander: func(
			*pgxpool.Pool,
		) (control.Commander, error) {
			return &controlBoundaryIntegrationCommander{},
				nil
		},

		newHandler: func(
			control.Commander,
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
			return &controlBoundaryIntegrationListener{},
				nil
		},

		newServer: func(
			controlSettings,
			http.Handler,
		) reportHTTPServer {
			server :=
				&controlBoundaryIntegrationServer{
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

func controlBoundaryIntegrationErrorName(
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
