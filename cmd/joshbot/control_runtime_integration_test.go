package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/control"
)

type controlRuntimeIntegrationDatabase struct{}

func (*controlRuntimeIntegrationDatabase) Close() {}

func (*controlRuntimeIntegrationDatabase) Ping(
	context.Context,
) error {
	return nil
}

func (*controlRuntimeIntegrationDatabase) Pool() *pgxpool.Pool {
	return nil
}

type controlRuntimeIntegrationCommander struct {
	paused chan bool
}

func (commander *controlRuntimeIntegrationCommander) SetProcessorPaused(
	_ context.Context,
	processor string,
	paused bool,
	_ control.Audit,
) error {
	if processor != "discovery" {
		return errors.New("unexpected processor")
	}

	commander.paused <- paused
	return nil
}

func (*controlRuntimeIntegrationCommander) AddDomainAvoid(
	context.Context,
	string,
	control.Audit,
) error {
	return nil
}

func (*controlRuntimeIntegrationCommander) RemoveDomainAvoid(
	context.Context,
	string,
	control.Audit,
) error {
	return nil
}

func (*controlRuntimeIntegrationCommander) SetOriginBlocked(
	context.Context,
	string,
	bool,
	control.Audit,
) error {
	return nil
}

func (*controlRuntimeIntegrationCommander) RecordRejected(
	context.Context,
	control.Audit,
) error {
	return nil
}

func TestControlRuntimeIntegrationServesOperatorAPI(
	t *testing.T,
) {
	const token = "0123456789abcdef0123456789abcdef"

	tokenFile := filepath.Join(
		t.TempDir(),
		"operator-token",
	)

	if err := os.WriteFile(
		tokenFile,
		[]byte(token+"\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write operator token: %v",
			err,
		)
	}

	commander := &controlRuntimeIntegrationCommander{
		paused: make(chan bool, 1),
	}

	listenerAddress := make(chan string, 1)

	dependencies := controlRuntimeDependencies{
		getenv: func(name string) string {
			switch name {
			case controlTokenFileEnvironment:
				return tokenFile
			case controlListenAddressEnvironment:
				return "127.0.0.1:8789"
			default:
				return ""
			}
		},
		withDatabase: func(
			_ context.Context,
			action func(databaseConnection) error,
		) error {
			return action(
				&controlRuntimeIntegrationDatabase{},
			)
		},
		readToken: readControlTokenFile,
		newCommander: func(
			*pgxpool.Pool,
		) (control.Commander, error) {
			return commander, nil
		},
		newHandler: control.NewHandler,
		listen: func(
			network string,
			_ string,
		) (net.Listener, error) {
			listener, err := net.Listen(
				network,
				"127.0.0.1:0",
			)
			if err == nil {
				listenerAddress <- listener.Addr().String()
			}
			return listener, err
		},
		newServer: newControlHTTPServer,
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runControl(
			ctx,
			dependencies,
		)
	}()

	var address string
	select {
	case address = <-listenerAddress:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal(
			"control listener did not start",
		)
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	healthResponse, err := client.Get(
		"http://" + address + "/healthz",
	)
	if err != nil {
		cancel()
		t.Fatalf(
			"GET /healthz error = %v",
			err,
		)
	}
	_, _ = io.Copy(
		io.Discard,
		healthResponse.Body,
	)
	_ = healthResponse.Body.Close()

	if healthResponse.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf(
			"GET /healthz status = %d, want %d",
			healthResponse.StatusCode,
			http.StatusOK,
		)
	}

	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+address+
			"/api/v1/control/processors/discovery/pause",
		strings.NewReader(`{}`),
	)
	if err != nil {
		cancel()
		t.Fatalf(
			"http.NewRequest() error = %v",
			err,
		)
	}

	request.Header.Set(
		"Authorization",
		"Bearer "+token,
	)
	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	response, err := client.Do(request)
	if err != nil {
		cancel()
		t.Fatalf(
			"POST pause error = %v",
			err,
		)
	}
	_, _ = io.Copy(
		io.Discard,
		response.Body,
	)
	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf(
			"POST pause status = %d, want %d",
			response.StatusCode,
			http.StatusOK,
		)
	}

	select {
	case paused := <-commander.paused:
		if !paused {
			cancel()
			t.Fatal(
				"pause mutation received paused=false",
			)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal(
			"control commander did not receive pause mutation",
		)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf(
				"runControl() error = %v, want nil",
				err,
			)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(
			"runControl() did not stop after cancellation",
		)
	}
}

func TestControlRuntimeIntegrationRejectsInvalidTokenFile(
	t *testing.T,
) {
	tokenFile := filepath.Join(
		t.TempDir(),
		"operator-token",
	)

	if err := os.WriteFile(
		tokenFile,
		[]byte("short\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write operator token: %v",
			err,
		)
	}

	dependencies := controlRuntimeDependencies{
		getenv: func(name string) string {
			if name == controlTokenFileEnvironment {
				return tokenFile
			}
			return ""
		},
		withDatabase: func(
			context.Context,
			func(databaseConnection) error,
		) error {
			t.Fatal(
				"database boundary reached with invalid token",
			)
			return nil
		},
		readToken: readControlTokenFile,
		newCommander: func(
			*pgxpool.Pool,
		) (control.Commander, error) {
			return nil, nil
		},
		newHandler: control.NewHandler,
		listen:     net.Listen,
		newServer:  newControlHTTPServer,
	}

	err := runControl(
		context.Background(),
		dependencies,
	)

	if !errors.Is(
		err,
		errInvalidControlToken,
	) {
		t.Fatalf(
			"runControl() error = %v, want %v",
			err,
			errInvalidControlToken,
		)
	}
}
