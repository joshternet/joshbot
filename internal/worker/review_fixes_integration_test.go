package worker

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestWorkerDefersRetryAfterWithoutOccupyingSlot(t *testing.T) {
	source := mustWorkerOrigin(t)
	queue := &fakeQueue{claims: []fakeClaim{{lease: workerTestLease(source), found: true}}}
	verifier := &fakeVerifier{result: declaration.Result{Origin: source, Outcome: declaration.OutcomeUnavailable, FailureCategory: retry.CategoryHTTP429, RetryAfter: retry.MaxDelay}}
	config := workerTestConfig()
	config.JobTimeout = 2 * time.Millisecond
	config.CompletionGrace = time.Millisecond
	runtime := newWorkerForTest(t, queue, verifier, config)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	worked, err := runtime.RunOnce(ctx)
	if !worked || err != nil || verifier.calls != 1 || queue.completeCalls != 1 {
		t.Fatalf("worked=%v err=%v calls=%v completed=%v", worked, err, verifier.calls, queue.completeCalls)
	}
	if queue.completedResults[0].RetryAfter != retry.MaxDelay {
		t.Fatal("Retry-After was not passed to durable completion")
	}
}
