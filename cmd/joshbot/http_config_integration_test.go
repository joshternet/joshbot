package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
)

func TestHTTPSettingsDefaultsAndOverridesIntegration(t *testing.T) {
	env := mapEnvironment{}
	got, err := loadHTTPConfig(env.get)
	if err != nil || got != robots.DefaultHTTPConfig() {
		t.Fatalf("config=%v error=%v", got, err)
	}
	for _, key := range []string{robotsMaxCrawlDelayEnvironment, httpDialTimeoutEnvironment, httpTLSHandshakeTimeoutEnvironment, httpResponseHeaderTimeoutEnvironment} {
		env[key] = "250ms"
	}
	got, err = loadHTTPConfig(env.get)
	want := robots.HTTPConfig{MaxCrawlDelay: 250 * time.Millisecond, DialTimeout: 250 * time.Millisecond, TLSHandshakeTimeout: 250 * time.Millisecond, ResponseHeaderTimeout: 250 * time.Millisecond}
	if err != nil || got != want {
		t.Fatalf("config=%v error=%v", got, err)
	}
}

func TestHTTPSettingsRejectInvalidValuesIntegration(t *testing.T) {
	for _, key := range []string{robotsMaxCrawlDelayEnvironment, httpDialTimeoutEnvironment, httpTLSHandshakeTimeoutEnvironment, httpResponseHeaderTimeoutEnvironment} {
		for _, value := range []string{"garbage", "0s", "-1s", "9223372036854775808ns"} {
			env := mapEnvironment{key: value}
			if _, err := loadHTTPConfig(env.get); !errors.Is(err, errInvalidRuntimeConfiguration) {
				t.Fatalf("%s=%s error=%v", key, value, err)
			}
		}
	}
}

func TestHTTPSettingsFailBeforeRuntimeActivityIntegration(t *testing.T) {
	for _, key := range []string{robotsMaxCrawlDelayEnvironment, httpDialTimeoutEnvironment, httpTLSHandshakeTimeoutEnvironment, httpResponseHeaderTimeoutEnvironment} {
		t.Run(key, func(t *testing.T) {
			env := validCrawlRuntimeEnvironment()
			env[key] = "0s"
			if _, err := loadCrawlRuntimeSettings(env.get); !errors.Is(err, errInvalidRuntimeConfiguration) {
				t.Fatalf("discovery error=%v", err)
			}
			operations := newRuntimeOperations(io.Discard)
			operations.getenv = env.get
			operations.newDatabase = func(context.Context, *pgxpool.Config) (databaseConnection, error) {
				t.Fatal("database activity with invalid limits")
				return nil, nil
			}
			if err := operations.worker(context.Background()); !errors.Is(err, errInvalidRuntimeConfiguration) {
				t.Fatalf("worker error=%v", err)
			}
			if err := executeWebBotAuthConformance(context.Background(), webBotAuthExpectUnregistered, io.Discard, env.get, &conformanceClient{}); !errors.Is(err, errInvalidRuntimeConfiguration) {
				t.Fatalf("conformance error=%v", err)
			}
		})
	}
}

func TestRuntimeHTTPSettingsReachEveryClientAndPropagateConstructionErrorIntegration(t *testing.T) {
	original := newRuntimeHTTPChecker
	t.Cleanup(func() { newRuntimeHTTPChecker = original })
	failure := errors.New("HTTP checker construction failed")
	want := robots.HTTPConfig{MaxCrawlDelay: 250 * time.Millisecond, DialTimeout: 250 * time.Millisecond, TLSHandshakeTimeout: 250 * time.Millisecond, ResponseHeaderTimeout: 250 * time.Millisecond}
	calls := 0
	newRuntimeHTTPChecker = func(_ netguard.Resolver, dialer netguard.Dialer, _ time.Duration, _ robots.RequestSigner, config robots.HTTPConfig) (*robots.Checker, error) {
		calls++
		if config != want {
			t.Fatalf("config=%v want %v", config, want)
		}
		if got := dialer.(*net.Dialer).Timeout; got != want.DialTimeout {
			t.Fatalf("dial timeout=%v", got)
		}
		return nil, failure
	}
	override := func(base environmentGetter) environmentGetter {
		return func(key string) string {
			switch key {
			case robotsMaxCrawlDelayEnvironment, httpDialTimeoutEnvironment, httpTLSHandshakeTimeoutEnvironment, httpResponseHeaderTimeoutEnvironment:
				return "250ms"
			}
			return base(key)
		}
	}
	operations, state := newTestRuntimeOperations(t)
	operations.getenv = override(operations.getenv)
	if err := operations.worker(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("worker error=%v", err)
	}
	if state.newWorkerCount != 0 {
		t.Fatal("worker started after checker failure")
	}
	settings, err := loadCrawlRuntimeSettings(override(validCrawlRuntimeEnvironment().get))
	if err != nil {
		t.Fatal(err)
	}
	_, err = newDiscoveryRuntimeWithStoreFactory(context.Background(), nil, settings, func(*pgxpool.Pool, store.AutomaticCrawlConfig) (discoveryRuntimeStore, error) {
		return &coverageDiscoveryRuntimeStore{}, nil
	})
	if !errors.Is(err, failure) {
		t.Fatalf("discovery error=%v", err)
	}
	_, _, keyPath := writeConformanceIdentity(t)
	env := mapEnvironment{webBotAuthModeEnvironment: webBotAuthModeRequired, webBotAuthActivePrivateKeyFileEnvironment: keyPath}
	if err := executeWebBotAuthConformance(context.Background(), webBotAuthExpectUnregistered, io.Discard, override(env.get), nil); !errors.Is(err, failure) {
		t.Fatalf("conformance error=%v", err)
	}
	if calls != 3 {
		t.Fatalf("constructed clients=%d want 3", calls)
	}
}
