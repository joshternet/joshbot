package main

import (
	"context"

	"github.com/joshternet/joshbot/internal/store"
)

func (*runtimeBoundaryIntegrationQueue) AbandonVerification(
	context.Context,
	store.Lease,
) error {
	return nil
}

func (*fakeRuntimeQueue) AbandonVerification(
	context.Context,
	store.Lease,
) error {
	return nil
}
