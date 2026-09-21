package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/webbotauth"
)

const cloudflareWebBotAuthTestURL = "https://crawltest.com/cdn-cgi/web-bot-auth"

var (
	webBotAuthConformanceDirectoryURL = webbotauth.SignatureAgentURL
	webBotAuthConformanceProbeURL     = cloudflareWebBotAuthTestURL
)

const (
	webBotAuthExpectUnregistered = "unregistered"
	webBotAuthExpectVerified     = "verified"
)

var errWebBotAuthConformance = fmt.Errorf(
	"web bot auth conformance failed",
)

type webBotAuthConformanceClient interface {
	Get(
		context.Context,
		*url.URL,
	) (*http.Response, error)
}

type webBotAuthFetchResult struct {
	status int
	header http.Header
	body   []byte
}

func runWebBotAuthConformanceCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	return runWebBotAuthConformance(
		ctx,
		args,
		stdout,
		stderr,
		os.Getenv,
		nil,
	)
}

func runWebBotAuthConformance(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	getenv environmentGetter,
	client webBotAuthConformanceClient,
) int {
	expectation, err := parseWebBotAuthConformanceArgs(args)
	if err != nil {
		return reportUsage(
			stderr,
			"conformance requires web-bot-auth --expect unregistered|verified",
		)
	}

	if err := executeWebBotAuthConformance(
		ctx,
		expectation,
		stdout,
		getenv,
		client,
	); err != nil {
		return reportCommandFailure(
			stderr,
			"conformance",
			err,
		)
	}

	return exitSuccess
}

func parseWebBotAuthConformanceArgs(
	args []string,
) (string, error) {
	if len(args) != 3 ||
		args[0] != "web-bot-auth" ||
		args[1] != "--expect" {
		return "", errWebBotAuthConformance
	}

	switch args[2] {
	case webBotAuthExpectUnregistered,
		webBotAuthExpectVerified:
		return args[2], nil
	default:
		return "", errWebBotAuthConformance
	}
}

func executeWebBotAuthConformance(
	ctx context.Context,
	expectation string,
	stdout io.Writer,
	getenv environmentGetter,
	client webBotAuthConformanceClient,
) error {
	config, err := loadWebBotAuthConfig(getenv)
	if err != nil {
		return err
	}

	if config.mode != webBotAuthModeRequired ||
		config.active == nil {
		return fmt.Errorf(
			"%w: unsigned mode is not allowed",
			errWebBotAuthConformance,
		)
	}

	signer, err := webBotAuthConformanceSigner(
		config.active,
	)
	if err != nil {
		return err
	}

	requestDelay, err := loadWorkerRequestDelay(getenv)
	if err != nil {
		return err
	}

	if client == nil {
		client = newWebBotAuthConformanceClient(
			requestDelay,
			signer,
		)
	}

	directoryURL, probeURL, err :=
		webBotAuthConformanceTargets()
	if err != nil {
		return err
	}

	authority, err := webBotAuthDirectoryAuthority(
		directoryURL,
	)
	if err != nil {
		return err
	}

	directory, err := fetchWebBotAuth(
		ctx,
		client,
		directoryURL,
		webbotauth.MaxDirectoryBodySize,
	)
	if err != nil {
		return err
	}

	report, err := webbotauth.ValidateDirectory(
		time.Now(),
		authority,
		directory.status,
		directory.header,
		directory.body,
	)
	if err != nil {
		return err
	}

	if !directoryPublishesKey(
		report,
		config.active.KeyID(),
	) {
		return fmt.Errorf(
			"%w: active key %s is not in the signature directory",
			errWebBotAuthConformance,
			config.active.KeyID(),
		)
	}

	transitionID := ""
	if config.transition != nil {
		transitionID = config.transition.KeyID()
		if !directoryPublishesKey(report, transitionID) {
			return fmt.Errorf(
				"%w: transition key %s is not in the signature directory",
				errWebBotAuthConformance,
				transitionID,
			)
		}
	}

	probe, err := fetchWebBotAuth(
		ctx,
		client,
		probeURL,
		webbotauth.MaxDirectoryBodySize,
	)
	if err != nil {
		return err
	}

	pass, err := cloudflareWebBotAuthResult(
		expectation,
		probe.status,
	)
	if err != nil {
		return err
	}

	writeWebBotAuthConformanceResult(
		stdout,
		report,
		config.active.KeyID(),
		transitionID,
		probe.status,
		pass,
	)

	return nil
}

var webBotAuthConformanceSigner = func(
	identity *webbotauth.Identity,
) (*webbotauth.Signer, error) {
	if identity == nil {
		return nil, fmt.Errorf(
			"%w: unsigned mode is not allowed",
			errWebBotAuthConformance,
		)
	}

	return identity.Signer()
}

func newWebBotAuthConformanceClient(
	requestDelay time.Duration,
	signer *webbotauth.Signer,
) webBotAuthConformanceClient {
	return robots.NewCheckerWithRequestDelayAndSigner(
		net.DefaultResolver,
		&net.Dialer{},
		requestDelay,
		signer,
	)
}

func webBotAuthConformanceTargets() (
	*url.URL,
	*url.URL,
	error,
) {
	directory, err := parseFixedHTTPSURL(
		webBotAuthConformanceDirectoryURL,
	)
	if err != nil {
		return nil, nil, err
	}

	probe, err := parseFixedHTTPSURL(
		webBotAuthConformanceProbeURL,
	)
	if err != nil {
		return nil, nil, err
	}

	return directory, probe, nil
}

func parseFixedHTTPSURL(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil ||
		target.Scheme != "https" ||
		target.Host == "" ||
		target.String() != raw {
		return nil, fmt.Errorf(
			"%w: HTTPS target is required",
			errWebBotAuthConformance,
		)
	}

	return target, nil
}

func webBotAuthDirectoryAuthority(
	target *url.URL,
) (string, error) {
	if target == nil {
		return "", fmt.Errorf(
			"%w: signature directory",
			errWebBotAuthConformance,
		)
	}

	canonical, err := origin.Parse(target.String())
	if err != nil {
		return "", err
	}

	return canonical.Hostname(), nil
}

func fetchWebBotAuth(
	ctx context.Context,
	client webBotAuthConformanceClient,
	target *url.URL,
	limit int64,
) (webBotAuthFetchResult, error) {
	if ctx == nil {
		return webBotAuthFetchResult{}, fmt.Errorf(
			"%w: context",
			errWebBotAuthConformance,
		)
	}

	if err := ctx.Err(); err != nil {
		return webBotAuthFetchResult{}, err
	}

	if client == nil || target == nil {
		return webBotAuthFetchResult{}, fmt.Errorf(
			"%w: request",
			errWebBotAuthConformance,
		)
	}

	response, err := client.Get(ctx, target)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return webBotAuthFetchResult{}, contextErr
		}

		return webBotAuthFetchResult{}, fmt.Errorf(
			"%w: %w",
			errWebBotAuthConformance,
			err,
		)
	}

	if response == nil || response.Body == nil {
		return webBotAuthFetchResult{}, fmt.Errorf(
			"%w: empty response",
			errWebBotAuthConformance,
		)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(
		io.LimitReader(response.Body, limit+1),
	)
	if err != nil {
		return webBotAuthFetchResult{}, fmt.Errorf(
			"%w: %w",
			errWebBotAuthConformance,
			err,
		)
	}

	if int64(len(body)) > limit {
		return webBotAuthFetchResult{}, fmt.Errorf(
			"%w: response is too large",
			errWebBotAuthConformance,
		)
	}

	return webBotAuthFetchResult{
		status: response.StatusCode,
		header: response.Header.Clone(),
		body:   body,
	}, nil
}

func directoryPublishesKey(
	report webbotauth.DirectoryReport,
	keyID string,
) bool {
	for _, id := range report.KeyIDs {
		if id == keyID {
			return true
		}
	}

	return false
}

func cloudflareWebBotAuthResult(
	expectation string,
	status int,
) (string, error) {
	switch status {
	case http.StatusBadRequest:
		return "", fmt.Errorf(
			"%w: Cloudflare rejected the Web Bot Auth request as malformed",
			errWebBotAuthConformance,
		)
	case http.StatusUnauthorized:
		if expectation == webBotAuthExpectUnregistered {
			return "pass: Cloudflare accepted the Web Bot Auth request shape and did not verify the identity.", nil
		}

		return "", fmt.Errorf(
			"%w: Cloudflare did not verify the Web Bot Auth identity",
			errWebBotAuthConformance,
		)
	case http.StatusOK:
		if expectation == webBotAuthExpectVerified {
			return "pass: Cloudflare verified the Web Bot Auth request.", nil
		}

		return "", fmt.Errorf(
			"%w: Cloudflare verified the Web Bot Auth request before registration was expected",
			errWebBotAuthConformance,
		)
	default:
		return "", fmt.Errorf(
			"%w: Cloudflare returned HTTP %d",
			errWebBotAuthConformance,
			status,
		)
	}
}

func writeWebBotAuthConformanceResult(
	stdout io.Writer,
	report webbotauth.DirectoryReport,
	activeKeyID string,
	transitionKeyID string,
	status int,
	pass string,
) {
	_, _ = fmt.Fprintf(
		stdout,
		"directory: %s\nkeys: %d\nstate: %s\nactive key: %s\n",
		webbotauth.SignatureAgentURL,
		len(report.KeyIDs),
		report.State,
		activeKeyID,
	)

	if transitionKeyID != "" {
		_, _ = fmt.Fprintf(
			stdout,
			"transition key: %s\n",
			transitionKeyID,
		)
	}

	_, _ = fmt.Fprintf(
		stdout,
		"cloudflare: %d\n%s\n",
		status,
		pass,
	)
}
