package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestLoadWorkerRequestDelay(
	t *testing.T,
) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{
			name: "default",
			want: defaultCrawlRequestDelay,
		},
		{
			name:  "configured",
			value: "250ms",
			want:  250 * time.Millisecond,
		},
		{
			name:  "zero",
			value: "0s",
			want:  0,
		},
		{
			name:    "invalid",
			value:   "eventually",
			wantErr: true,
		},
		{
			name:    "negative",
			value:   "-1s",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := map[string]string{}
			if test.value != "" {
				environment[crawlRequestDelayEnvironment] =
					test.value
			}

			getenv := func(name string) string {
				return environment[name]
			}

			got, err := loadWorkerRequestDelay(
				getenv,
			)

			if test.wantErr {
				if !errors.Is(
					err,
					errInvalidRuntimeConfiguration,
				) {
					t.Fatalf(
						"loadWorkerRequestDelay() error = %v, want invalid configuration",
						err,
					)
				}

				if got != 0 {
					t.Errorf(
						"delay = %v on error, want 0",
						got,
					)
				}

				return
			}

			if err != nil {
				t.Fatalf(
					"loadWorkerRequestDelay() error = %v, want nil",
					err,
				)
			}

			if got != test.want {
				t.Errorf(
					"delay = %v, want %v",
					got,
					test.want,
				)
			}
		})
	}
}

func TestWorkerRejectsInvalidCrawlRequestDelay(
	t *testing.T,
) {
	operations := newRuntimeOperations(
		io.Discard,
	)

	environment := map[string]string{
		workerIDEnvironment:          "worker-test",
		crawlRequestDelayEnvironment: "invalid",
	}

	operations.getenv = func(name string) string {
		return environment[name]
	}

	err := operations.worker(
		context.Background(),
	)

	if !errors.Is(
		err,
		errInvalidRuntimeConfiguration,
	) {
		t.Fatalf(
			"worker() error = %v, want invalid configuration",
			err,
		)
	}
}
