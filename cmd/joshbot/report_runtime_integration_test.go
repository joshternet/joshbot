package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/reporting"
)

type reportRuntimeIntegrationDatabase struct{}

func (*reportRuntimeIntegrationDatabase) Close() {}

func (*reportRuntimeIntegrationDatabase) Ping(
	context.Context,
) error {
	return nil
}

func (*reportRuntimeIntegrationDatabase) Pool() *pgxpool.Pool {
	return nil
}

type reportRuntimeIntegrationReader struct {
	statusCalls chan struct{}
}

func (reader *reportRuntimeIntegrationReader) Status(
	context.Context,
) (reporting.Status, error) {
	reader.statusCalls <- struct{}{}
	return reporting.Status{}, nil
}

func (*reportRuntimeIntegrationReader) Sources(
	context.Context,
	int,
) ([]reporting.CrawlSource, error) {
	return nil, nil
}

func (*reportRuntimeIntegrationReader) Source(
	context.Context,
	string,
	int,
) (reporting.SourceDetail, bool, error) {
	return reporting.SourceDetail{}, false, nil
}

func (*reportRuntimeIntegrationReader) Crawls(
	context.Context,
	int,
) ([]reporting.CrawlRun, error) {
	return nil, nil
}

func (*reportRuntimeIntegrationReader) Crawl(
	context.Context,
	int64,
) (reporting.CrawlDetail, bool, error) {
	return reporting.CrawlDetail{}, false, nil
}

func (*reportRuntimeIntegrationReader) Queue(
	context.Context,
	int,
) ([]reporting.QueueItem, error) {
	return nil, nil
}

func (*reportRuntimeIntegrationReader) QueueEvents(
	context.Context,
	int,
) ([]reporting.QueueEvent, error) {
	return nil, nil
}

func (*reportRuntimeIntegrationReader) Audits(
	context.Context,
	int,
) ([]reporting.AuditEvent, error) {
	return nil, nil
}

func (*reportRuntimeIntegrationReader) Services(
	context.Context,
) ([]reporting.ServiceStatus, error) {
	return nil, nil
}

func TestReportRuntimeIntegrationServesPrivateAPI(
	t *testing.T,
) {
	const token = "integration-report-token"

	tokenFile := filepath.Join(
		t.TempDir(),
		"report-token",
	)

	if err := os.WriteFile(
		tokenFile,
		[]byte(token+"\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write report token: %v",
			err,
		)
	}

	reader := &reportRuntimeIntegrationReader{
		statusCalls: make(chan struct{}, 1),
	}

	listenerAddress := make(chan string, 1)

	dependencies := reportRuntimeDependencies{
		getenv: func(name string) string {
			switch name {
			case reportTokenFileEnvironment:
				return tokenFile
			case reportListenAddressEnvironment:
				return "127.0.0.1:8788"
			default:
				return ""
			}
		},
		withDatabase: func(
			_ context.Context,
			action func(databaseConnection) error,
		) error {
			return action(
				&reportRuntimeIntegrationDatabase{},
			)
		},
		readToken: readReportTokenFile,
		newReader: func(
			*pgxpool.Pool,
			reporting.PostgresConfig,
		) (reporting.Reader, error) {
			return reader, nil
		},
		newHandler: newReportHandler,
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
		newServer: newReportHTTPServer,
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runReport(
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
			"report listener did not start",
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
		http.MethodGet,
		"http://"+address+"/api/v1/status",
		nil,
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

	response, err := client.Do(request)
	if err != nil {
		cancel()
		t.Fatalf(
			"GET /api/v1/status error = %v",
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
			"GET /api/v1/status status = %d, want %d",
			response.StatusCode,
			http.StatusOK,
		)
	}

	select {
	case <-reader.statusCalls:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal(
			"report reader did not receive status request",
		)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf(
				"runReport() error = %v, want nil",
				err,
			)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(
			"runReport() did not stop after cancellation",
		)
	}
}

func TestReportRuntimeIntegrationRejectsEmptyTokenFile(
	t *testing.T,
) {
	tokenFile := filepath.Join(
		t.TempDir(),
		"report-token",
	)

	if err := os.WriteFile(
		tokenFile,
		nil,
		0o600,
	); err != nil {
		t.Fatalf(
			"write report token: %v",
			err,
		)
	}

	dependencies := reportRuntimeDependencies{
		getenv: func(name string) string {
			if name == reportTokenFileEnvironment {
				return tokenFile
			}
			return ""
		},
		withDatabase: func(
			context.Context,
			func(databaseConnection) error,
		) error {
			t.Fatal(
				"database boundary reached with empty token",
			)
			return nil
		},
		readToken: readReportTokenFile,
		newReader: func(
			*pgxpool.Pool,
			reporting.PostgresConfig,
		) (reporting.Reader, error) {
			return nil, nil
		},
		newHandler: newReportHandler,
		listen:     net.Listen,
		newServer:  newReportHTTPServer,
	}

	err := runReport(
		context.Background(),
		dependencies,
	)

	if !errors.Is(
		err,
		errEmptyReportToken,
	) {
		t.Fatalf(
			"runReport() error = %v, want %v",
			err,
			errEmptyReportToken,
		)
	}
}
