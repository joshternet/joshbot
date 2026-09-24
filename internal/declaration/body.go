package declaration

import (
	"errors"
	"fmt"
	"io"
)

var errDeclarationBodyTooLarge = errors.New(
	"declaration: response body too large",
)

func readDeclarationBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(
		body,
		int64(MaxBodySize)+1,
	)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf(
			"declaration: read response body: %w",
			err,
		)
	}

	if len(data) > MaxBodySize {
		return nil, errDeclarationBodyTooLarge
	}

	return data, nil
}
