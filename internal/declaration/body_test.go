package declaration

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadDeclarationBodyAcceptsExactLimit(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, MaxBodySize)

	got, err := readDeclarationBody(bytes.NewReader(body))
	if err != nil {
		t.Fatalf(
			"readDeclarationBody() error = %v, want nil",
			err,
		)
	}

	if !bytes.Equal(got, body) {
		t.Fatal(
			"readDeclarationBody() did not return the complete body",
		)
	}
}

func TestReadDeclarationBodyRejectsOversizeWithoutOverreading(
	t *testing.T,
) {
	body := &countingDeclarationReader{
		remaining: MaxBodySize + 100,
	}

	got, err := readDeclarationBody(body)
	if !errors.Is(err, errDeclarationBodyTooLarge) {
		t.Fatalf(
			"readDeclarationBody() error = %v, "+
				"want errDeclarationBodyTooLarge",
			err,
		)
	}

	if got != nil {
		t.Errorf(
			"readDeclarationBody() body length = %d, want nil",
			len(got),
		)
	}

	if body.read != MaxBodySize+1 {
		t.Errorf(
			"body bytes read = %d, want %d",
			body.read,
			MaxBodySize+1,
		)
	}
}

func TestReadDeclarationBodyReturnsReadFailure(t *testing.T) {
	readFailure := errors.New("test declaration body read failure")
	body := &failingDeclarationReader{
		data: []byte(`{"version":1}`),
		err:  readFailure,
	}

	got, err := readDeclarationBody(body)
	if !errors.Is(err, readFailure) {
		t.Fatalf(
			"readDeclarationBody() error = %v, want read failure",
			err,
		)
	}

	if got != nil {
		t.Errorf(
			"readDeclarationBody() body = %q, want nil",
			got,
		)
	}
}

func TestReadDeclarationBodyRejectsNilBody(t *testing.T) {
	got, err := readDeclarationBody(nil)
	if !errors.Is(err, errDeclarationBodyUnavailable) {
		t.Fatalf(
			"readDeclarationBody() error = %v, "+
				"want errDeclarationBodyUnavailable",
			err,
		)
	}

	if got != nil {
		t.Errorf(
			"readDeclarationBody() body = %q, want nil",
			got,
		)
	}
}

type countingDeclarationReader struct {
	remaining int
	read      int
}

func (r *countingDeclarationReader) Read(
	destination []byte,
) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}

	count := min(len(destination), r.remaining)
	for index := range count {
		destination[index] = 'x'
	}

	r.remaining -= count
	r.read += count

	return count, nil
}

type failingDeclarationReader struct {
	data []byte
	err  error
}

func (r *failingDeclarationReader) Read(
	destination []byte,
) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}

	count := copy(destination, r.data)
	r.data = r.data[count:]

	return count, nil
}
