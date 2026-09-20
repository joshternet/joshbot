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

type controlTestCommander struct{}

func (*controlTestCommander) SetProcessorPaused(context.Context, string, bool, control.Audit) error {
	return nil
}
func (*controlTestCommander) AddDomainAvoid(context.Context, string, control.Audit) error {
	return nil
}
func (*controlTestCommander) RemoveDomainAvoid(context.Context, string, control.Audit) error {
	return nil
}
func (*controlTestCommander) SetOriginBlocked(context.Context, string, bool, control.Audit) error {
	return nil
}
func (*controlTestCommander) RecordRejected(context.Context, control.Audit) error {
	return nil
}

func TestLoadControlSettingsRequiresIndependentToken(t *testing.T) {
	settings, err := loadControlSettings(reportEnvironment(map[string]string{
		controlTokenFileEnvironment:     "/run/secrets/joshbot_operator_token",
		controlListenAddressEnvironment: "0.0.0.0:8789",
		reportTokenFileEnvironment:      "/run/secrets/joshbot_report_token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if settings.tokenFile != "/run/secrets/joshbot_operator_token" ||
		settings.listenAddress != "0.0.0.0:8789" {
		t.Errorf("settings = %#v", settings)
	}
	for _, environment := range []map[string]string{
		{reportTokenFileEnvironment: "/report-only"},
		{controlTokenFileEnvironment: " ", reportTokenFileEnvironment: "/report"},
		{controlTokenFileEnvironment: "/operator", controlListenAddressEnvironment: "invalid"},
	} {
		if _, err := loadControlSettings(reportEnvironment(environment)); err == nil {
			t.Errorf("environment %#v accepted", environment)
		}
	}
}

func TestReadControlTokenRequiresHighEntropyBoundedValue(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef"
	for _, raw := range []string{valid, valid + "\n", valid + "\r\n"} {
		got, err := readControlToken(strings.NewReader(raw))
		if err != nil || got != valid {
			t.Errorf("readControlToken(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"",
		"short",
		strings.Repeat("x", minControlTokenSize),
		strings.Repeat("x", maxControlTokenSize+1),
		"0123456789abcdef\n0123456789abcdef",
	} {
		if _, err := readControlToken(strings.NewReader(raw)); err == nil {
			t.Errorf("readControlToken(%q) error = nil", raw)
		}
	}
}

func TestRunControlBuildsIndependentBoundary(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef"
	commander := &controlTestCommander{}
	var handlerToken string
	dependencies := controlRuntimeDependencies{
		getenv: reportEnvironment(map[string]string{
			controlTokenFileEnvironment: "/operator",
		}),
		withDatabase: func(_ context.Context, action func(databaseConnection) error) error {
			return action(&reportTestDatabase{})
		},
		readToken: func(path string) (string, error) {
			if path != "/operator" {
				t.Errorf("token path = %q", path)
			}
			return valid, nil
		},
		newCommander: func(*pgxpool.Pool) (control.Commander, error) {
			return commander, nil
		},
		newHandler: func(got control.Commander, token string) (http.Handler, error) {
			if got != commander {
				t.Error("commander mismatch")
			}
			handlerToken = token
			return &reportTestHandler{}, nil
		},
		listen: func(string, string) (net.Listener, error) {
			return nil, errors.New("stop after construction")
		},
		newServer: func(controlSettings, http.Handler) reportHTTPServer {
			return nil
		},
	}
	if err := runControl(context.Background(), dependencies); err == nil {
		t.Fatal("runControl() error = nil")
	}
	if handlerToken != valid {
		t.Errorf("handler token = %q", handlerToken)
	}
}

func TestControlRuntimeRejectsMissingDependencies(t *testing.T) {
	if err := runControl(context.Background(), controlRuntimeDependencies{}); !errors.Is(err, errControlRuntimeUnavailable) {
		t.Errorf("error = %v", err)
	}
}

func TestControlRuntimeAndConstructors(t *testing.T) {
	if err := (runtimeOperations{}).control(context.Background()); !errors.Is(err, errControlRuntimeUnavailable) {
		t.Errorf("control() error = %v", err)
	}
	if _, err := newControlCommander(nil); err == nil {
		t.Error("newControlCommander(nil) error = nil")
	}
	settings := controlSettings{
		listenAddress:     "127.0.0.1:8789",
		readHeaderTimeout: time.Second,
		readTimeout:       2 * time.Second,
		writeTimeout:      3 * time.Second,
		idleTimeout:       4 * time.Second,
	}
	handler := &reportTestHandler{}
	server, ok := newControlHTTPServer(settings, handler).(*http.Server)
	if !ok {
		t.Fatal("newControlHTTPServer() did not return *http.Server")
	}
	if server.Addr != settings.listenAddress || server.Handler != handler ||
		server.ReadHeaderTimeout != settings.readHeaderTimeout ||
		server.ReadTimeout != settings.readTimeout ||
		server.WriteTimeout != settings.writeTimeout ||
		server.IdleTimeout != settings.idleTimeout {
		t.Errorf("server = %#v", server)
	}
}

func TestLoadControlSettingsRejectsInvalidConfiguration(t *testing.T) {
	if _, err := loadControlSettings(nil); !errors.Is(err, errInvalidControlConfiguration) {
		t.Errorf("nil getenv error = %v", err)
	}
	for _, address := range []string{"localhost", "localhost:nope", "localhost:0", "localhost:65536"} {
		_, err := loadControlSettings(reportEnvironment(map[string]string{
			controlTokenFileEnvironment:     "/token",
			controlListenAddressEnvironment: address,
		}))
		if !errors.Is(err, errInvalidControlConfiguration) {
			t.Errorf("address %q error = %v", address, err)
		}
	}
}

func TestReadControlTokenFileAndReaderFailures(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	valid := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(tokenFile, []byte(valid+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readControlTokenFile(tokenFile); err != nil || got != valid {
		t.Errorf("readControlTokenFile() = %q, %v", got, err)
	}
	if _, err := readControlTokenFile(tokenFile + ".missing"); !errors.Is(err, errOpenControlTokenFile) {
		t.Errorf("missing file error = %v", err)
	}
	if _, err := readControlToken(nil); !errors.Is(err, errControlTokenReaderUnavailable) {
		t.Errorf("nil reader error = %v", err)
	}
	if _, err := readControlToken(failingReportReader{}); !errors.Is(err, errReadControlTokenFile) {
		t.Errorf("reader failure = %v", err)
	}
	for _, token := range []string{
		" 123456789abcdef0123456789abcdef",
		"0123456789abcdef0123456789abcde ",
		"0123456789abcdef0123456789abcde\x7f",
	} {
		if validControlToken(token) {
			t.Errorf("validControlToken(%q) = true", token)
		}
	}
}

func TestRunControlPreservesFailures(t *testing.T) {
	expected := errors.New("test failure")
	tests := []struct {
		name   string
		change func(*controlRuntimeDependencies)
	}{
		{"nil context", func(*controlRuntimeDependencies) {}},
		{"settings", func(dependencies *controlRuntimeDependencies) {
			dependencies.getenv = reportEnvironment(map[string]string{})
		}},
		{"token", func(dependencies *controlRuntimeDependencies) {
			dependencies.readToken = func(string) (string, error) { return "", expected }
		}},
		{"database", func(dependencies *controlRuntimeDependencies) {
			dependencies.withDatabase = func(context.Context, func(databaseConnection) error) error { return expected }
		}},
		{"nil database", func(dependencies *controlRuntimeDependencies) {
			dependencies.withDatabase = func(_ context.Context, action func(databaseConnection) error) error {
				return action(nil)
			}
		}},
		{"commander", func(dependencies *controlRuntimeDependencies) {
			dependencies.newCommander = func(*pgxpool.Pool) (control.Commander, error) { return nil, expected }
		}},
		{"handler", func(dependencies *controlRuntimeDependencies) {
			dependencies.newHandler = func(control.Commander, string) (http.Handler, error) { return nil, expected }
		}},
		{"listener", func(dependencies *controlRuntimeDependencies) {
			dependencies.listen = func(string, string) (net.Listener, error) { return nil, expected }
		}},
		{"server", func(dependencies *controlRuntimeDependencies) {
			dependencies.newServer = func(controlSettings, http.Handler) reportHTTPServer { return nil }
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := validControlDependencies()
			test.change(&dependencies)
			ctx := context.Background()
			if test.name == "nil context" {
				ctx = nil
			}
			if err := runControl(ctx, dependencies); err == nil {
				t.Fatal("runControl() error = nil")
			}
		})
	}
}

func TestRunControlServesAndClosesListener(t *testing.T) {
	dependencies := validControlDependencies()
	listener := &reportTestListener{}
	server := &reportTestServer{serveResult: make(chan error, 1)}
	server.serveResult <- http.ErrServerClosed
	dependencies.listen = func(string, string) (net.Listener, error) { return listener, nil }
	dependencies.newServer = func(controlSettings, http.Handler) reportHTTPServer { return server }
	if err := runControl(context.Background(), dependencies); err != nil {
		t.Fatal(err)
	}
	if !listener.closed || server.serveCalls != 1 {
		t.Errorf("listener closed = %v, serve calls = %d", listener.closed, server.serveCalls)
	}
}

func TestServeControl(t *testing.T) {
	listener := &reportTestListener{}
	if err := serveControl(nil, nil, nil, 0); !errors.Is(err, errInvalidControlConfiguration) {
		t.Errorf("invalid error = %v", err)
	}
	for _, result := range []error{nil, http.ErrServerClosed} {
		server := &reportTestServer{serveResult: make(chan error, 1)}
		server.serveResult <- result
		if err := serveControl(context.Background(), server, listener, time.Second); err != nil {
			t.Errorf("serve result %v error = %v", result, err)
		}
	}
	server := &reportTestServer{serveResult: make(chan error, 1)}
	server.serveResult <- errors.New("serve failed")
	if err := serveControl(context.Background(), server, listener, time.Second); err == nil {
		t.Error("serve failure error = nil")
	}
	for _, test := range []struct {
		name        string
		shutdownErr error
		stopResult  error
		wantError   bool
	}{
		{"closed", nil, http.ErrServerClosed, false},
		{"nil", nil, nil, false},
		{"shutdown failure", errors.New("shutdown failed"), nil, true},
		{"stop failure", nil, errors.New("stop failed"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &reportTestServer{
				serveResult:         make(chan error, 1),
				shutdownErr:         test.shutdownErr,
				shutdownServeResult: test.stopResult,
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := serveControl(ctx, server, listener, time.Second)
			if test.shutdownErr != nil {
				server.serveResult <- http.ErrServerClosed
			}
			if (err != nil) != test.wantError {
				t.Errorf("error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func validControlDependencies() controlRuntimeDependencies {
	return controlRuntimeDependencies{
		getenv: reportEnvironment(map[string]string{
			controlTokenFileEnvironment: "/token",
		}),
		withDatabase: func(_ context.Context, action func(databaseConnection) error) error {
			return action(&reportTestDatabase{})
		},
		readToken: func(string) (string, error) {
			return "0123456789abcdef0123456789abcdef", nil
		},
		newCommander: func(*pgxpool.Pool) (control.Commander, error) {
			return &controlTestCommander{}, nil
		},
		newHandler: func(control.Commander, string) (http.Handler, error) {
			return &reportTestHandler{}, nil
		},
		listen: func(string, string) (net.Listener, error) {
			return &reportTestListener{}, nil
		},
		newServer: func(controlSettings, http.Handler) reportHTTPServer {
			server := &reportTestServer{serveResult: make(chan error, 1)}
			server.serveResult <- http.ErrServerClosed
			return server
		},
	}
}
