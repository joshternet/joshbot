package main

import (
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
)

func TestNewDiscoveryRuntimeReturnsRunnerConstructionFailure(
	t *testing.T,
) {
	_, pool := newCLIIntegrationEnvironment(t)

	runner, err := newDiscoveryRuntime(
		pool,
		discovery.Config{},
	)
	if err == nil {
		t.Fatal(
			"newDiscoveryRuntime() error = nil, want non-nil",
		)
	}

	if runner != nil {
		t.Errorf(
			"newDiscoveryRuntime() runner = %#v, want nil",
			runner,
		)
	}

	if !strings.Contains(
		err.Error(),
		"construct discovery runner",
	) {
		t.Errorf(
			"newDiscoveryRuntime() error = %v, want runner construction context",
			err,
		)
	}
}
