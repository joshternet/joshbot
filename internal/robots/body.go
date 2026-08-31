package robots

import (
	"errors"
	"fmt"
	"io"
)

// MaxBodySize is JoshBot's maximum accepted robots body size.
//
// RFC 9309 requires a supported parsing limit of at least 500 KiB.
// JoshBot accepts exactly 500 KiB and fails closed above that limit.
const MaxBodySize = 500 * 1024

var (
	errBodyUnavailable = errors.New("robots: body unavailable")
	errBodyTooLarge    = errors.New("robots: body exceeds 500 KiB")
)

func readPolicy(body io.Reader) (Policy, error) {
	if body == nil {
		return disallowAllPolicy(), errBodyUnavailable
	}

	limited := io.LimitReader(body, int64(MaxBodySize)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return disallowAllPolicy(), fmt.Errorf(
			"robots: read body: %w",
			err,
		)
	}

	if len(data) > MaxBodySize {
		return disallowAllPolicy(), errBodyTooLarge
	}

	return Parse(data), nil
}

func disallowAllPolicy() Policy {
	return Policy{
		rules: []rule{
			{
				pattern:     "*",
				allow:       false,
				specificity: 1,
			},
		},
	}
}
