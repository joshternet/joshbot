package robots

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadPolicyAcceptsExactBodyLimit(t *testing.T) {
	rules := []byte(
		"User-agent: Joshternet-Joshbot\n" +
			"Disallow: /private\n",
	)
	body := make([]byte, MaxBodySize)
	copy(body, rules)

	for index := len(rules); index < len(body); index++ {
		body[index] = '#'
	}

	policy, err := readPolicy(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("readPolicy() error = %v, want nil", err)
	}

	target := mustTarget(t, "https://example.com/private/page")
	if policy.Allowed(target) {
		t.Fatal("Policy.Allowed() = true, want false")
	}
}

func TestReadPolicyRejectsOversizedBodyWithoutOverreading(
	t *testing.T,
) {
	body := &countingBody{
		remaining: MaxBodySize + 100,
	}

	policy, err := readPolicy(body)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf(
			"readPolicy() error = %v, want errBodyTooLarge",
			err,
		)
	}

	if body.read != MaxBodySize+1 {
		t.Errorf(
			"body bytes read = %d, want %d",
			body.read,
			MaxBodySize+1,
		)
	}

	target := mustTarget(t, "https://example.com/public")
	if policy.Allowed(target) {
		t.Fatal("oversized-body Policy.Allowed() = true, want false")
	}
}

func TestReadPolicyFailsClosedOnReadError(t *testing.T) {
	readFailure := errors.New("test body read failure")
	body := &failingBody{
		data: []byte(
			"User-agent: Joshternet-Joshbot\n" +
				"Allow: /\n",
		),
		err: readFailure,
	}

	policy, err := readPolicy(body)
	if !errors.Is(err, readFailure) {
		t.Fatalf(
			"readPolicy() error = %v, want wrapped read failure",
			err,
		)
	}

	target := mustTarget(t, "https://example.com/public")
	if policy.Allowed(target) {
		t.Fatal("read-failure Policy.Allowed() = true, want false")
	}
}

func TestReadPolicyFailsClosedWithoutBody(t *testing.T) {
	policy, err := readPolicy(nil)
	if !errors.Is(err, errBodyUnavailable) {
		t.Fatalf(
			"readPolicy() error = %v, want errBodyUnavailable",
			err,
		)
	}

	target := mustTarget(t, "https://example.com/public")
	if policy.Allowed(target) {
		t.Fatal("missing-body Policy.Allowed() = true, want false")
	}
}

type countingBody struct {
	remaining int
	read      int
}

func (b *countingBody) Read(destination []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}

	count := min(len(destination), b.remaining)
	for index := range count {
		destination[index] = '#'
	}

	b.remaining -= count
	b.read += count

	return count, nil
}

type failingBody struct {
	data []byte
	err  error
}

func (b *failingBody) Read(destination []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, b.err
	}

	count := copy(destination, b.data)
	b.data = b.data[count:]

	return count, nil
}

func (*failingBody) Close() error {
	return nil
}
