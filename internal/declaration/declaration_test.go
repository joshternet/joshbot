package declaration

import (
	"strings"
	"testing"
)

func TestParseMinimumDeclaration(t *testing.T) {
	got := Parse([]byte(`{"version":1}`))
	want := ParseResult{
		Status: ParseValid,
		Declaration: Declaration{
			Version:  1,
			Identity: IdentityUndeclared,
		},
	}

	if got != want {
		t.Errorf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseValidDeclarations(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		identity Identity
	}{
		{
			name:     "undeclared",
			data:     `{"version":1}`,
			identity: IdentityUndeclared,
		},
		{
			name: "affirmed",
			data: `{
				"version": 1,
				"josh": true
			}`,
			identity: IdentityAffirmed,
		},
		{
			name: "declined",
			data: `{
				"version": 1,
				"josh": false
			}`,
			identity: IdentityDeclined,
		},
		{
			name: "reordered members",
			data: `{
				"josh": true,
				"version": 1
			}`,
			identity: IdentityAffirmed,
		},
		{
			name: "surrounding whitespace",
			data: "\n\t {\r\n" +
				"\t\"version\": 1,\r\n" +
				"\t\"josh\": false\r\n" +
				"} \t\n",
			identity: IdentityDeclined,
		},
		{
			name: "unknown scalar",
			data: `{
				"version": 1,
				"favoriteColor": "blue",
				"numberOfTabsOpen": 47
			}`,
			identity: IdentityUndeclared,
		},
		{
			name: "unknown object",
			data: `{
				"version": 1,
				"something": {
					"nested": {
						"value": true
					}
				}
			}`,
			identity: IdentityUndeclared,
		},
		{
			name: "unknown array",
			data: `{
				"version": 1,
				"something": [1, 2, {"three": 3}]
			}`,
			identity: IdentityUndeclared,
		},
		{
			name: "participating is ignored",
			data: `{
				"version": 1,
				"participating": false
			}`,
			identity: IdentityUndeclared,
		},
		{
			name: "case variant Josh is unknown",
			data: `{
				"version": 1,
				"Josh": true,
				"JOSH": false
			}`,
			identity: IdentityUndeclared,
		},
		{
			name: "Unicode unknown member",
			data: `{
				"version": 1,
				"greeting": "こんにちは"
			}`,
			identity: IdentityUndeclared,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Parse([]byte(test.data))
			want := ParseResult{
				Status: ParseValid,
				Declaration: Declaration{
					Version:  1,
					Identity: test.identity,
				},
			}

			if got != want {
				t.Errorf(
					"Parse() = %#v, want %#v",
					got,
					want,
				)
			}
		})
	}
}

func TestParseRejectsInvalidDeclarations(t *testing.T) {
	invalidName := append(
		[]byte(`{"version":1,"`),
		0xff,
	)
	invalidName = append(
		invalidName,
		[]byte(`":true}`)...,
	)

	invalidValue := append(
		[]byte(`{"version":1,"unknown":"`),
		0xff,
	)
	invalidValue = append(
		invalidValue,
		[]byte(`"}`)...,
	)

	bom := append(
		[]byte{0xef, 0xbb, 0xbf},
		[]byte(`{"version":1}`)...,
	)

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "missing version",
			data: []byte(`{}`),
		},
		{
			name: "case variant Version",
			data: []byte(`{"Version":1}`),
		},
		{
			name: "version string",
			data: []byte(`{"version":"1"}`),
		},
		{
			name: "version boolean",
			data: []byte(`{"version":true}`),
		},
		{
			name: "version null",
			data: []byte(`{"version":null}`),
		},
		{
			name: "version float",
			data: []byte(`{"version":1.0}`),
		},
		{
			name: "version exponent",
			data: []byte(`{"version":1e0}`),
		},
		{
			name: "version leading zero",
			data: []byte(`{"version":01}`),
		},
		{
			name: "josh string",
			data: []byte(`{"version":1,"josh":"true"}`),
		},
		{
			name: "josh number",
			data: []byte(`{"version":1,"josh":1}`),
		},
		{
			name: "josh null",
			data: []byte(`{"version":1,"josh":null}`),
		},
		{
			name: "josh object",
			data: []byte(`{"version":1,"josh":{}}`),
		},
		{
			name: "josh array",
			data: []byte(`{"version":1,"josh":[]}`),
		},
		{
			name: "top-level null",
			data: []byte(`null`),
		},
		{
			name: "top-level boolean",
			data: []byte(`true`),
		},
		{
			name: "top-level number",
			data: []byte(`1`),
		},
		{
			name: "top-level array",
			data: []byte(`[]`),
		},
		{
			name: "top-level string",
			data: []byte(`"Josh"`),
		},
		{
			name: "empty body",
			data: nil,
		},
		{
			name: "malformed JSON",
			data: []byte(`{"version":1`),
		},
		{
			name: "trailing JSON value",
			data: []byte(`{"version":1} true`),
		},
		{
			name: "trailing garbage",
			data: []byte(`{"version":1} garbage`),
		},
		{
			name: "invalid UTF-8 member name",
			data: invalidName,
		},
		{
			name: "invalid UTF-8 unknown value",
			data: invalidValue,
		},
		{
			name: "unpaired Unicode surrogate",
			data: []byte(
				`{"version":1,"unknown":"\uDEAD"}`,
			),
		},
		{
			name: "initial UTF-8 BOM",
			data: bom,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Parse(test.data)
			if got.Status != ParseInvalid {
				t.Errorf(
					"Parse() status = %v, want ParseInvalid",
					got.Status,
				)
			}

			if got.Declaration != (Declaration{}) {
				t.Errorf(
					"Parse() declaration = %#v, want zero value",
					got.Declaration,
				)
			}
		})
	}
}

func TestParseRejectsDuplicateMemberNames(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "duplicate version",
			data: `{
				"version": 1,
				"version": 1
			}`,
		},
		{
			name: "escaped duplicate version",
			data: `{
				"version": 1,
				"\u0076ersion": 1
			}`,
		},
		{
			name: "duplicate josh",
			data: `{
				"version": 1,
				"josh": true,
				"josh": false
			}`,
		},
		{
			name: "escaped duplicate josh",
			data: `{
				"version": 1,
				"josh": true,
				"\u006aosh": false
			}`,
		},
		{
			name: "duplicate unknown member",
			data: `{
				"version": 1,
				"unknown": 1,
				"unknown": 2
			}`,
		},
		{
			name: "duplicate inside unknown object",
			data: `{
				"version": 1,
				"unknown": {
					"name": 1,
					"n\u0061me": 2
				}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Parse([]byte(test.data))
			if got.Status != ParseInvalid {
				t.Errorf(
					"Parse() status = %v, want ParseInvalid",
					got.Status,
				)
			}

			if got.Declaration != (Declaration{}) {
				t.Errorf(
					"Parse() declaration = %#v, want zero value",
					got.Declaration,
				)
			}
		})
	}
}

func TestParseReportsUnsupportedVersions(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "zero",
			data: `{"version":0}`,
		},
		{
			name: "two",
			data: `{"version":2}`,
		},
		{
			name: "negative",
			data: `{"version":-1}`,
		},
		{
			name: "negative zero",
			data: `{"version":-0}`,
		},
		{
			name: "large integer",
			data: `{
				"version":
				1234567890123456789012345678901234567890
			}`,
		},
		{
			name: "unsupported version does not interpret josh",
			data: `{
				"version": 2,
				"josh": "not version 1 semantics"
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Parse([]byte(test.data))
			if got.Status != ParseUnsupportedVersion {
				t.Errorf(
					"Parse() status = %v, "+
						"want ParseUnsupportedVersion",
					got.Status,
				)
			}

			if got.Declaration != (Declaration{}) {
				t.Errorf(
					"Parse() declaration = %#v, want zero value",
					got.Declaration,
				)
			}
		})
	}
}

func TestParseRejectsExcessiveNesting(t *testing.T) {
	const nestingDepth = 10000

	data := []byte(
		`{"version":1,"unknown":` +
			strings.Repeat("[", nestingDepth) +
			`0` +
			strings.Repeat("]", nestingDepth) +
			`}`,
	)

	if len(data) > MaxBodySize {
		t.Fatalf(
			"test body size = %d, want at most %d",
			len(data),
			MaxBodySize,
		)
	}

	got := Parse(data)
	if got.Status != ParseInvalid {
		t.Errorf(
			"Parse() status = %v, want ParseInvalid",
			got.Status,
		)
	}
}

func TestParseEnforcesBodyLimit(t *testing.T) {
	minimum := []byte(`{"version":1}`)
	exact := make([]byte, MaxBodySize)
	copy(exact, minimum)

	for index := len(minimum); index < len(exact); index++ {
		exact[index] = ' '
	}

	got := Parse(exact)
	want := ParseResult{
		Status: ParseValid,
		Declaration: Declaration{
			Version:  1,
			Identity: IdentityUndeclared,
		},
	}
	if got != want {
		t.Errorf(
			"Parse(exact limit) = %#v, want %#v",
			got,
			want,
		)
	}

	oversized := append(
		append([]byte(nil), exact...),
		' ',
	)
	got = Parse(oversized)
	if got.Status != ParseInvalid {
		t.Errorf(
			"Parse(oversized) status = %v, want ParseInvalid",
			got.Status,
		)
	}
}
