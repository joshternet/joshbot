package declaration

import (
	"bytes"
	"testing"
)

func FuzzParseDeclaration(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"version":1}`),
		[]byte(`{"version":1,"josh":true}`),
		[]byte(`{"version":1,"josh":false}`),
		[]byte(`{}`),
		[]byte(`[]`),
		[]byte(`{"version":2,"josh":true}`),
		[]byte(`{"version":1,"version":1}`),
		[]byte(`{"version":1,"josh":true,"josh":false}`),
		[]byte(
			`{"version":1,"josh":true,"\u006aosh":false}`,
		),
		[]byte(`{"version":1`),
		[]byte(`{"version":1} true`),
		[]byte(
			`{"version":1,"unknown":{"nested":[1,2,3]}}`,
		),
		{0xff},
		{
			0xef,
			0xbb,
			0xbf,
			'{',
			'"',
			'v',
			'e',
			'r',
			's',
			'i',
			'o',
			'n',
			'"',
			':',
			'1',
			'}',
		},
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		original := append([]byte(nil), data...)

		first := Parse(data)
		second := Parse(data)

		if !bytes.Equal(data, original) {
			t.Fatal("Parse() modified its input")
		}

		if first != second {
			t.Fatalf(
				"Parse() is nondeterministic: first = %#v, second = %#v",
				first,
				second,
			)
		}

		switch first.Status {
		case ParseValid:
			if first.Declaration.Version != 1 {
				t.Errorf(
					"valid declaration version = %d, want 1",
					first.Declaration.Version,
				)
			}

			switch first.Declaration.Identity {
			case IdentityUndeclared,
				IdentityAffirmed,
				IdentityDeclined:
			default:
				t.Errorf(
					"valid declaration identity = %d, want a defined identity",
					first.Declaration.Identity,
				)
			}

		case ParseInvalid, ParseUnsupportedVersion:
			if first.Declaration != (Declaration{}) {
				t.Errorf(
					"non-valid declaration = %#v, want zero value",
					first.Declaration,
				)
			}

		default:
			t.Fatalf(
				"Parse() status = %d, want a defined status",
				first.Status,
			)
		}
	})
}
