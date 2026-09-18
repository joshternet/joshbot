package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/reporting"
)

const (
	reportListenAddressEnvironment = "JOSHBOT_REPORT_LISTEN_ADDRESS"
	reportTokenFileEnvironment     = "JOSHBOT_REPORT_TOKEN_FILE"

	defaultReportListenAddress = "127.0.0.1:8788"

	defaultReportReadHeaderTimeout = 5 * time.Second
	defaultReportReadTimeout       = 10 * time.Second
	defaultReportWriteTimeout      = 30 * time.Second
	defaultReportIdleTimeout       = 60 * time.Second
	defaultReportShutdownTimeout   = 10 * time.Second

	maxReportTokenSize = 4096
)

var (
	errInvalidReportConfiguration = errors.New(
		"report: invalid configuration",
	)
	errReportRuntimeUnavailable = errors.New(
		"report: runtime dependencies are unavailable",
	)
	errReportDatabaseUnavailable = errors.New(
		"report: database connection is unavailable",
	)
	errReportServerUnavailable = errors.New(
		"report: HTTP server is unavailable",
	)
	errReportTokenReaderUnavailable = errors.New(
		"report: token reader is unavailable",
	)
	errOpenReportTokenFile = errors.New(
		"report: cannot open token file",
	)
	errReadReportTokenFile = errors.New(
		"report: cannot read token file",
	)
	errEmptyReportToken = errors.New(
		"report: token file is empty",
	)
	errReportTokenTooLarge = errors.New(
		"report: token is too large",
	)
	errInvalidReportToken = errors.New(
		"report: token is invalid",
	)
)

type reportSettings struct {
	listenAddress string
	tokenFile     string

	automaticCrawlEnabled bool
	maxPendingProbes      int

	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
}

type reportHTTPServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
}

type reportRuntimeDependencies struct {
	getenv environmentGetter

	withDatabase func(
		context.Context,
		func(databaseConnection) error,
	) error

	readToken func(string) (string, error)

	newReader func(
		*pgxpool.Pool,
		reporting.PostgresConfig,
	) (reporting.Reader, error)

	newHandler func(
		reporting.Reader,
		string,
	) (http.Handler, error)

	listen func(
		string,
		string,
	) (net.Listener, error)

	newServer func(
		reportSettings,
		http.Handler,
	) reportHTTPServer
}

func (operations runtimeOperations) report(
	ctx context.Context,
) error {
	return runReport(
		ctx,
		reportRuntimeDependencies{
			getenv:       operations.getenv,
			withDatabase: operations.withDatabase,
			readToken:    readReportTokenFile,
			newReader:    newReportReader,
			newHandler:   newReportHandler,
			listen:       net.Listen,
			newServer:    newReportHTTPServer,
		},
	)
}

func runReport(
	ctx context.Context,
	dependencies reportRuntimeDependencies,
) error {
	if ctx == nil {
		return errInvalidReportConfiguration
	}

	if !validReportRuntimeDependencies(
		dependencies,
	) {
		return errReportRuntimeUnavailable
	}

	settings, err := loadReportSettings(
		dependencies.getenv,
	)
	if err != nil {
		return err
	}

	token, err := dependencies.readToken(
		settings.tokenFile,
	)
	if err != nil {
		return err
	}

	return dependencies.withDatabase(
		ctx,
		func(
			connection databaseConnection,
		) error {
			if connection == nil {
				return errReportDatabaseUnavailable
			}

			reader, err := dependencies.newReader(
				connection.Pool(),
				reporting.PostgresConfig{
					AutomaticCrawlEnabled: settings.automaticCrawlEnabled,
					MaxPendingProbes:      settings.maxPendingProbes,
				},
			)
			if err != nil {
				return fmt.Errorf(
					"construct reporting reader: %w",
					err,
				)
			}

			handler, err := dependencies.newHandler(
				reader,
				token,
			)
			if err != nil {
				return fmt.Errorf(
					"construct reporting handler: %w",
					err,
				)
			}

			listener, err := dependencies.listen(
				"tcp",
				settings.listenAddress,
			)
			if err != nil {
				return fmt.Errorf(
					"listen for reporting: %w",
					err,
				)
			}
			defer listener.Close()

			server := dependencies.newServer(
				settings,
				handler,
			)
			if server == nil {
				return errReportServerUnavailable
			}

			return serveReport(
				ctx,
				server,
				listener,
				settings.shutdownTimeout,
			)
		},
	)
}

func loadReportSettings(
	getenv environmentGetter,
) (reportSettings, error) {
	if getenv == nil {
		return reportSettings{},
			errInvalidReportConfiguration
	}

	listenAddress := settingOrDefault(
		getenv,
		reportListenAddressEnvironment,
		defaultReportListenAddress,
	)

	_, rawPort, err := net.SplitHostPort(
		listenAddress,
	)
	if err != nil {
		return reportSettings{},
			fmt.Errorf(
				"%w: %s must be a TCP host and port",
				errInvalidReportConfiguration,
				reportListenAddressEnvironment,
			)
	}

	port, err := strconv.Atoi(rawPort)
	if err != nil ||
		port < 1 ||
		port > 65535 {
		return reportSettings{},
			fmt.Errorf(
				"%w: %s port must be between 1 and 65535",
				errInvalidReportConfiguration,
				reportListenAddressEnvironment,
			)
	}

	tokenFile := getenv(
		reportTokenFileEnvironment,
	)
	if strings.TrimSpace(tokenFile) == "" {
		return reportSettings{},
			fmt.Errorf(
				"%w: %s is required",
				errInvalidReportConfiguration,
				reportTokenFileEnvironment,
			)
	}

	automaticEnabled, err := optionalBooleanSetting(
		getenv,
		automaticCrawlEnabledEnvironment,
	)
	if err != nil {
		return reportSettings{}, err
	}

	maxPendingProbes :=
		defaultAutomaticCrawlMaxPendingProbes

	if automaticEnabled {
		maxPendingProbes, err =
			optionalPositiveIntegerSetting(
				getenv,
				automaticCrawlMaxPendingProbesEnvironment,
				defaultAutomaticCrawlMaxPendingProbes,
			)
		if err != nil {
			return reportSettings{}, err
		}
	}

	return reportSettings{
		listenAddress: listenAddress,
		tokenFile:     tokenFile,

		automaticCrawlEnabled: automaticEnabled,
		maxPendingProbes:      maxPendingProbes,

		readHeaderTimeout: defaultReportReadHeaderTimeout,
		readTimeout:       defaultReportReadTimeout,
		writeTimeout:      defaultReportWriteTimeout,
		idleTimeout:       defaultReportIdleTimeout,
		shutdownTimeout:   defaultReportShutdownTimeout,
	}, nil
}

func readReportTokenFile(
	tokenFile string,
) (string, error) {
	file, err := os.Open(tokenFile)
	if err != nil {
		return "", errOpenReportTokenFile
	}
	defer file.Close()

	return readReportToken(file)
}

func readReportToken(
	reader io.Reader,
) (string, error) {
	if reader == nil {
		return "", errReportTokenReaderUnavailable
	}

	limited := io.LimitReader(
		reader,
		int64(maxReportTokenSize)+3,
	)

	data, err := io.ReadAll(limited)
	if err != nil {
		return "", errReadReportTokenFile
	}

	if len(data) > 0 &&
		data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}

	if len(data) > 0 &&
		data[len(data)-1] == '\r' {
		data = data[:len(data)-1]
	}

	if len(data) == 0 {
		return "", errEmptyReportToken
	}

	if len(data) > maxReportTokenSize {
		return "", errReportTokenTooLarge
	}

	token := string(data)

	if strings.ContainsAny(
		token,
		"\r\n",
	) {
		return "", errInvalidReportToken
	}

	return token, nil
}

func newReportReader(
	pool *pgxpool.Pool,
	config reporting.PostgresConfig,
) (reporting.Reader, error) {
	return reporting.NewPostgresReader(
		pool,
		config,
	)
}

func newReportHandler(
	reader reporting.Reader,
	token string,
) (http.Handler, error) {
	return reporting.NewHandler(
		reader,
		token,
	)
}

func newReportHTTPServer(
	settings reportSettings,
	handler http.Handler,
) reportHTTPServer {
	return &http.Server{
		Addr: settings.listenAddress,

		Handler: handler,

		ReadHeaderTimeout: settings.readHeaderTimeout,
		ReadTimeout:       settings.readTimeout,
		WriteTimeout:      settings.writeTimeout,
		IdleTimeout:       settings.idleTimeout,
	}
}

func serveReport(
	ctx context.Context,
	server reportHTTPServer,
	listener net.Listener,
	shutdownTimeout time.Duration,
) error {
	if ctx == nil ||
		server == nil ||
		listener == nil ||
		shutdownTimeout <= 0 {
		return errInvalidReportConfiguration
	}

	serverErrors := make(
		chan error,
		1,
	)

	go func() {
		serverErrors <- server.Serve(
			listener,
		)
	}()

	select {
	case err := <-serverErrors:
		if err == nil ||
			errors.Is(
				err,
				http.ErrServerClosed,
			) {
			return nil
		}

		return fmt.Errorf(
			"serve reporting API: %w",
			err,
		)

	case <-ctx.Done():
		shutdownContext, cancel :=
			context.WithTimeout(
				context.Background(),
				shutdownTimeout,
			)
		defer cancel()

		if err := server.Shutdown(
			shutdownContext,
		); err != nil {
			return fmt.Errorf(
				"shutdown reporting API: %w",
				err,
			)
		}

		err := <-serverErrors

		if err == nil ||
			errors.Is(
				err,
				http.ErrServerClosed,
			) {
			return nil
		}

		return fmt.Errorf(
			"stop reporting API: %w",
			err,
		)
	}
}

func validReportRuntimeDependencies(
	dependencies reportRuntimeDependencies,
) bool {
	return dependencies.getenv != nil &&
		dependencies.withDatabase != nil &&
		dependencies.readToken != nil &&
		dependencies.newReader != nil &&
		dependencies.newHandler != nil &&
		dependencies.listen != nil &&
		dependencies.newServer != nil
}
