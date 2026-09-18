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
	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/store"
)

const (
	controlListenAddressEnvironment = "JOSHBOT_CONTROL_LISTEN_ADDRESS"
	controlTokenFileEnvironment     = "JOSHBOT_OPERATOR_TOKEN_FILE"
	defaultControlListenAddress     = "127.0.0.1:8789"
	maxControlTokenSize             = 4096
	minControlTokenSize             = 32

	defaultControlReadHeaderTimeout = 5 * time.Second
	defaultControlReadTimeout       = 10 * time.Second
	defaultControlWriteTimeout      = 30 * time.Second
	defaultControlIdleTimeout       = 60 * time.Second
	defaultControlShutdownTimeout   = 10 * time.Second
)

var (
	errInvalidControlConfiguration   = errors.New("control: invalid configuration")
	errControlRuntimeUnavailable     = errors.New("control: runtime dependencies are unavailable")
	errControlDatabaseUnavailable    = errors.New("control: database connection is unavailable")
	errControlServerUnavailable      = errors.New("control: HTTP server is unavailable")
	errControlTokenReaderUnavailable = errors.New("control: token reader is unavailable")
	errOpenControlTokenFile          = errors.New("control: cannot open token file")
	errReadControlTokenFile          = errors.New("control: cannot read token file")
	errInvalidControlToken           = errors.New("control: token is invalid")
)

type controlSettings struct {
	listenAddress string
	tokenFile     string

	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
}

type controlRuntimeDependencies struct {
	getenv       environmentGetter
	withDatabase func(context.Context, func(databaseConnection) error) error
	readToken    func(string) (string, error)
	newCommander func(*pgxpool.Pool) (control.Commander, error)
	newHandler   func(control.Commander, string) (http.Handler, error)
	listen       func(string, string) (net.Listener, error)
	newServer    func(controlSettings, http.Handler) reportHTTPServer
}

func (operations runtimeOperations) control(ctx context.Context) error {
	return runControl(ctx, controlRuntimeDependencies{
		getenv:       operations.getenv,
		withDatabase: operations.withDatabase,
		readToken:    readControlTokenFile,
		newCommander: newControlCommander,
		newHandler:   control.NewHandler,
		listen:       net.Listen,
		newServer:    newControlHTTPServer,
	})
}

func runControl(ctx context.Context, dependencies controlRuntimeDependencies) error {
	if ctx == nil {
		return errInvalidControlConfiguration
	}
	if !validControlRuntimeDependencies(dependencies) {
		return errControlRuntimeUnavailable
	}
	settings, err := loadControlSettings(dependencies.getenv)
	if err != nil {
		return err
	}
	token, err := dependencies.readToken(settings.tokenFile)
	if err != nil {
		return err
	}
	return dependencies.withDatabase(ctx, func(connection databaseConnection) error {
		if connection == nil {
			return errControlDatabaseUnavailable
		}
		commander, err := dependencies.newCommander(connection.Pool())
		if err != nil {
			return fmt.Errorf("construct control store: %w", err)
		}
		handler, err := dependencies.newHandler(commander, token)
		if err != nil {
			return fmt.Errorf("construct control handler: %w", err)
		}
		listener, err := dependencies.listen("tcp", settings.listenAddress)
		if err != nil {
			return fmt.Errorf("listen for control API: %w", err)
		}
		defer listener.Close()
		server := dependencies.newServer(settings, handler)
		if server == nil {
			return errControlServerUnavailable
		}
		return serveControl(ctx, server, listener, settings.shutdownTimeout)
	})
}

func loadControlSettings(getenv environmentGetter) (controlSettings, error) {
	if getenv == nil {
		return controlSettings{}, errInvalidControlConfiguration
	}
	listenAddress := settingOrDefault(
		getenv,
		controlListenAddressEnvironment,
		defaultControlListenAddress,
	)
	_, rawPort, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return controlSettings{}, fmt.Errorf(
			"%w: %s must be a TCP host and port",
			errInvalidControlConfiguration,
			controlListenAddressEnvironment,
		)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return controlSettings{}, fmt.Errorf(
			"%w: %s port must be between 1 and 65535",
			errInvalidControlConfiguration,
			controlListenAddressEnvironment,
		)
	}
	tokenFile := getenv(controlTokenFileEnvironment)
	if strings.TrimSpace(tokenFile) == "" {
		return controlSettings{}, fmt.Errorf(
			"%w: %s is required",
			errInvalidControlConfiguration,
			controlTokenFileEnvironment,
		)
	}
	return controlSettings{
		listenAddress:     listenAddress,
		tokenFile:         tokenFile,
		readHeaderTimeout: defaultControlReadHeaderTimeout,
		readTimeout:       defaultControlReadTimeout,
		writeTimeout:      defaultControlWriteTimeout,
		idleTimeout:       defaultControlIdleTimeout,
		shutdownTimeout:   defaultControlShutdownTimeout,
	}, nil
}

func readControlTokenFile(tokenFile string) (string, error) {
	file, err := os.Open(tokenFile)
	if err != nil {
		return "", errOpenControlTokenFile
	}
	defer file.Close()
	return readControlToken(file)
}

func readControlToken(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errControlTokenReaderUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(maxControlTokenSize)+3))
	if err != nil {
		return "", errReadControlTokenFile
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	if len(data) > 0 && data[len(data)-1] == '\r' {
		data = data[:len(data)-1]
	}
	token := string(data)
	if !validControlToken(token) {
		return "", errInvalidControlToken
	}
	return token, nil
}

func validControlToken(token string) bool {
	if len(token) < minControlTokenSize || len(token) > maxControlTokenSize ||
		strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
		return false
	}
	unique := make(map[rune]struct{})
	for _, character := range token {
		if character < '!' || character > '~' {
			return false
		}
		unique[character] = struct{}{}
	}
	return len(unique) >= 8
}

func newControlCommander(pool *pgxpool.Pool) (control.Commander, error) {
	return store.NewControlStore(pool)
}

func newControlHTTPServer(settings controlSettings, handler http.Handler) reportHTTPServer {
	return &http.Server{
		Addr:              settings.listenAddress,
		Handler:           handler,
		ReadHeaderTimeout: settings.readHeaderTimeout,
		ReadTimeout:       settings.readTimeout,
		WriteTimeout:      settings.writeTimeout,
		IdleTimeout:       settings.idleTimeout,
		MaxHeaderBytes:    16 * 1024,
	}
}

func serveControl(
	ctx context.Context,
	server reportHTTPServer,
	listener net.Listener,
	shutdownTimeout time.Duration,
) error {
	if ctx == nil || server == nil || listener == nil || shutdownTimeout <= 0 {
		return errInvalidControlConfiguration
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()
	select {
	case err := <-serverErrors:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve control API: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			shutdownTimeout,
		)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown control API: %w", err)
		}
		err := <-serverErrors
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("stop control API: %w", err)
	}
}

func validControlRuntimeDependencies(dependencies controlRuntimeDependencies) bool {
	return dependencies.getenv != nil &&
		dependencies.withDatabase != nil &&
		dependencies.readToken != nil &&
		dependencies.newCommander != nil &&
		dependencies.newHandler != nil &&
		dependencies.listen != nil &&
		dependencies.newServer != nil
}
