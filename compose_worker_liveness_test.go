package joshbot

import (
	"os"
	"strings"
	"testing"
)

func TestWorkerUsesContainerLivenessInsteadOfDatabaseHealthcheck(
	t *testing.T,
) {
	contents, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf(
			"read compose.yaml: %v",
			err,
		)
	}

	worker, found := composeServiceBlock(
		string(contents),
		"worker",
	)
	if !found {
		t.Fatal(
			"compose.yaml does not define worker service",
		)
	}

	if strings.Contains(
		worker,
		"\n    healthcheck:",
	) {
		t.Error(
			"worker service defines a healthcheck; " +
				"container process state should be its liveness signal",
		)
	}

	if !strings.Contains(
		worker,
		"\n    restart: unless-stopped\n",
	) {
		t.Error(
			"worker service does not preserve restart: unless-stopped",
		)
	}
}

func TestComposePinsStableServiceInstanceIDs(t *testing.T) {
	contents, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}

	compose := string(contents)
	worker, workerFound := composeServiceBlock(compose, "worker")
	discovery, discoveryFound := composeServiceBlock(compose, "discovery")
	if !workerFound || !discoveryFound {
		t.Fatal("compose.yaml is missing worker or discovery")
	}

	if !strings.Contains(
		worker,
		"JOSHBOT_SERVICE_INSTANCE_ID: ${JOSHBOT_WORKER_SERVICE_INSTANCE_ID:-worker}",
	) {
		t.Error("worker service does not pin a stable heartbeat slot")
	}
	if !strings.Contains(
		discovery,
		"JOSHBOT_SERVICE_INSTANCE_ID: ${JOSHBOT_DISCOVERY_SERVICE_INSTANCE_ID:-discovery}",
	) {
		t.Error("discovery service does not pin a stable heartbeat slot")
	}
	if strings.Contains(worker, "JOSHBOT_DISCOVERY_SERVICE_INSTANCE_ID") ||
		strings.Contains(discovery, "JOSHBOT_WORKER_SERVICE_INSTANCE_ID") {
		t.Error("worker and discovery heartbeat slots are coupled")
	}
}

func composeServiceBlock(
	compose string,
	service string,
) (string, bool) {
	lines := strings.Split(compose, "\n")
	serviceHeader := "  " + service + ":"

	start := -1

	for index, line := range lines {
		if line == serviceHeader {
			start = index

			break
		}
	}

	if start < 0 {
		return "", false
	}

	end := len(lines)

	for index := start + 1; index < len(lines); index++ {
		line := lines[index]

		if strings.HasPrefix(line, "  ") &&
			!strings.HasPrefix(line, "    ") &&
			strings.HasSuffix(line, ":") {
			end = index

			break
		}

		if line != "" &&
			!strings.HasPrefix(line, " ") {
			end = index

			break
		}
	}

	return strings.Join(
		lines[start:end],
		"\n",
	), true
}
