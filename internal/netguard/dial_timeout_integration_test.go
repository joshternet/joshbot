package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestDialTimeoutFallsBackToNextValidatedAddressIntegration(t *testing.T) {
	destination := Destination{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}, port: 443}
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dialer := &fakeDialer{}
	var first context.Context
	dialer.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		if len(dialer.calls) == 1 {
			first = ctx
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if parent.Err() != nil {
			t.Fatal("first attempt consumed parent budget")
		}
		return pipeConnection(t), nil
	}
	conn, err := destination.DialContextWithTimeout(parent, dialer, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if len(dialer.calls) != 2 || !errors.Is(first.Err(), context.DeadlineExceeded) {
		t.Fatalf("calls=%v first=%v", dialer.calls, first.Err())
	}
}

func TestDialTimeoutFailuresAndParentDeadlineIntegration(t *testing.T) {
	destination := Destination{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}, port: 443}
	dialer := &fakeDialer{dial: func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }}
	if _, err := destination.DialContextWithTimeout(context.Background(), dialer, 0); !errors.Is(err, errInvalidDialTimeout) {
		t.Fatalf("error=%v", err)
	}
	if _, err := destination.DialContextWithTimeout(context.Background(), dialer, time.Millisecond); !errors.Is(err, errDialFailed) {
		t.Fatalf("error=%v", err)
	}
	if len(dialer.calls) != 2 {
		t.Fatalf("calls=%d", len(dialer.calls))
	}
	dialer.calls = nil
	parent, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := destination.DialContextWithTimeout(parent, dialer, time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if len(dialer.calls) != 1 {
		t.Fatalf("calls=%d", len(dialer.calls))
	}
}
