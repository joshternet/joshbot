package publicdata

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/testutil"
)

func TestBuildProducesEmptyRegistry(t *testing.T) {
	got, err := Build(nil)
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	want := []File{
		{
			Path: "registry.json",
			Data: []byte(
				"{\n" +
					"  \"format_version\": 1,\n" +
					"  \"nodes\": []\n" +
					"}\n",
			),
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf(
			"Build() = %#v, want %#v",
			got,
			want,
		)
	}
}

func TestBuildProducesKnownAffirmedNode(t *testing.T) {
	participant := verifiedParticipant(
		t,
		"https://example.com",
		declaration.IdentityAffirmed,
	)

	got, err := Build([]store.VerifiedOrigin{participant})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	const nodePath = "nodes/10/" +
		"100680ad546ce6a577f42f52df33b4cf" +
		"dca756859e664b8d7de329b150d09ce9.json"

	if len(got) != 2 {
		t.Fatalf("Build() file count = %d, want 2", len(got))
	}

	node := findPublicFile(t, got, nodePath)
	wantNode := "{\n" +
		"  \"format_version\": 1,\n" +
		"  \"origin\": \"https://example.com\",\n" +
		"  \"declaration\": {\n" +
		"    \"version\": 1,\n" +
		"    \"josh\": true\n" +
		"  }\n" +
		"}\n"

	if string(node.Data) != wantNode {
		t.Errorf(
			"node data =\n%s\nwant:\n%s",
			node.Data,
			wantNode,
		)
	}

	registry := findPublicFile(
		t,
		got,
		"registry.json",
	)
	wantRegistry := "{\n" +
		"  \"format_version\": 1,\n" +
		"  \"nodes\": [\n" +
		"    {\n" +
		"      \"origin\": \"https://example.com\",\n" +
		"      \"path\": \"" + nodePath + "\",\n" +
		"      \"declaration\": {\n" +
		"        \"version\": 1,\n" +
		"        \"josh\": true\n" +
		"      }\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"

	if string(registry.Data) != wantRegistry {
		t.Errorf(
			"registry data =\n%s\nwant:\n%s",
			registry.Data,
			wantRegistry,
		)
	}
}

func TestBuildRepresentsAllIdentityStates(t *testing.T) {
	tests := []struct {
		name         string
		identity     declaration.Identity
		wantJosh     string
		wantNoMember bool
	}{
		{
			name:         "undeclared",
			identity:     declaration.IdentityUndeclared,
			wantNoMember: true,
		},
		{
			name:     "affirmed",
			identity: declaration.IdentityAffirmed,
			wantJosh: "\"josh\": true",
		},
		{
			name:     "declined",
			identity: declaration.IdentityDeclined,
			wantJosh: "\"josh\": false",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files, err := Build(
				[]store.VerifiedOrigin{
					verifiedParticipant(
						t,
						"https://example.com",
						test.identity,
					),
				},
			)
			if err != nil {
				t.Fatalf(
					"Build() error = %v, want nil",
					err,
				)
			}

			for _, file := range files {
				data := string(file.Data)
				if test.wantNoMember {
					if strings.Contains(
						data,
						"\"josh\"",
					) {
						t.Errorf(
							"%s unexpectedly contains josh member",
							file.Path,
						)
					}
					continue
				}

				if !strings.Contains(
					data,
					test.wantJosh,
				) {
					t.Errorf(
						"%s does not contain %q",
						file.Path,
						test.wantJosh,
					)
				}
			}
		})
	}
}

func TestBuildSortsOriginsAndIgnoresInputOrder(
	t *testing.T,
) {
	participants := []store.VerifiedOrigin{
		verifiedParticipant(
			t,
			"https://example.org",
			declaration.IdentityDeclined,
		),
		verifiedParticipant(
			t,
			"http://example.com",
			declaration.IdentityUndeclared,
		),
		verifiedParticipant(
			t,
			"https://example.com:8443",
			declaration.IdentityAffirmed,
		),
		verifiedParticipant(
			t,
			"https://example.com",
			declaration.IdentityAffirmed,
		),
	}

	first, err := Build(participants)
	if err != nil {
		t.Fatalf("first Build() error = %v, want nil", err)
	}

	reordered := []store.VerifiedOrigin{
		participants[2],
		participants[0],
		participants[3],
		participants[1],
	}
	second, err := Build(reordered)
	if err != nil {
		t.Fatalf("second Build() error = %v, want nil", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Errorf(
			"Build() changed with input order:\nfirst: %#v\nsecond: %#v",
			first,
			second,
		)
	}

	registry := string(
		findPublicFile(
			t,
			first,
			"registry.json",
		).Data,
	)
	wantOrder := []string{
		"http://example.com",
		"https://example.com",
		"https://example.com:8443",
		"https://example.org",
	}

	lastIndex := -1
	for _, canonical := range wantOrder {
		index := strings.Index(
			registry,
			"\"origin\": \""+canonical+"\"",
		)
		if index <= lastIndex {
			t.Errorf(
				"registry origin %q index = %d after %d",
				canonical,
				index,
				lastIndex,
			)
		}
		lastIndex = index
	}

	for index := 1; index < len(first); index++ {
		if first[index-1].Path >= first[index].Path {
			t.Errorf(
				"file order %q then %q is not ascending",
				first[index-1].Path,
				first[index].Path,
			)
		}
	}
}

func TestBuildKeepsNodePathAcrossIdentityChanges(
	t *testing.T,
) {
	identities := []declaration.Identity{
		declaration.IdentityUndeclared,
		declaration.IdentityAffirmed,
		declaration.IdentityDeclined,
	}

	var paths []string
	var data [][]byte

	for _, identity := range identities {
		files, err := Build(
			[]store.VerifiedOrigin{
				verifiedParticipant(
					t,
					"https://example.com",
					identity,
				),
			},
		)
		if err != nil {
			t.Fatalf(
				"Build() error = %v, want nil",
				err,
			)
		}

		node := onlyNodeFile(t, files)
		paths = append(paths, node.Path)
		data = append(data, node.Data)
	}

	for _, path := range paths[1:] {
		if path != paths[0] {
			t.Errorf(
				"node path = %q, want %q",
				path,
				paths[0],
			)
		}
	}

	if bytes.Equal(data[0], data[1]) ||
		bytes.Equal(data[1], data[2]) ||
		bytes.Equal(data[0], data[2]) {
		t.Error(
			"identity changes did not change node bytes",
		)
	}
}

func TestBuildRejectsInvalidInput(t *testing.T) {
	valid := verifiedParticipant(
		t,
		"https://example.com",
		declaration.IdentityAffirmed,
	)

	tests := []struct {
		name      string
		input     []store.VerifiedOrigin
		wantError error
	}{
		{
			name: "zero origin",
			input: []store.VerifiedOrigin{
				{
					Declaration: declaration.Declaration{
						Version:  1,
						Identity: declaration.IdentityAffirmed,
					},
				},
			},
			wantError: ErrInvalidVerifiedOrigin,
		},
		{
			name: "zero version",
			input: []store.VerifiedOrigin{
				{
					Origin: valid.Origin,
					Declaration: declaration.Declaration{
						Identity: declaration.IdentityAffirmed,
					},
				},
			},
			wantError: ErrInvalidVerifiedOrigin,
		},
		{
			name: "unsupported version",
			input: []store.VerifiedOrigin{
				{
					Origin: valid.Origin,
					Declaration: declaration.Declaration{
						Version:  2,
						Identity: declaration.IdentityAffirmed,
					},
				},
			},
			wantError: ErrInvalidVerifiedOrigin,
		},
		{
			name: "unknown identity",
			input: []store.VerifiedOrigin{
				{
					Origin: valid.Origin,
					Declaration: declaration.Declaration{
						Version:  1,
						Identity: declaration.Identity(255),
					},
				},
			},
			wantError: ErrInvalidVerifiedOrigin,
		},
		{
			name: "duplicate origin",
			input: []store.VerifiedOrigin{
				valid,
				valid,
			},
			wantError: ErrDuplicateOrigin,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Build(test.input)
			if !errors.Is(err, test.wantError) {
				t.Errorf(
					"Build() error = %v, want %v",
					err,
					test.wantError,
				)
			}

			if got != nil {
				t.Errorf(
					"Build() = %#v, want nil",
					got,
				)
			}
		})
	}
}

func TestBuildUsesSafeHashedNodePaths(t *testing.T) {
	participants := []store.VerifiedOrigin{
		verifiedParticipant(
			t,
			"https://[2001:db8::1]",
			declaration.IdentityUndeclared,
		),
		verifiedParticipant(
			t,
			"https://example.com:8443",
			declaration.IdentityAffirmed,
		),
		verifiedParticipant(
			t,
			"https://xn--bcher-kva.example",
			declaration.IdentityDeclined,
		),
	}

	files, err := Build(participants)
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	pattern := regexp.MustCompile(
		`^nodes/[0-9a-f]{2}/[0-9a-f]{64}\.json$`,
	)

	nodeCount := 0
	for _, file := range files {
		if file.Path == "registry.json" {
			continue
		}

		nodeCount++
		if !pattern.MatchString(file.Path) {
			t.Errorf(
				"node path = %q, want lowercase SHA-256 path",
				file.Path,
			)
		}

		for _, forbidden := range []string{
			":",
			"[",
			"]",
			"example",
			"bücher",
			"xn--",
		} {
			if strings.Contains(file.Path, forbidden) {
				t.Errorf(
					"node path %q contains %q",
					file.Path,
					forbidden,
				)
			}
		}
	}

	if nodeCount != len(participants) {
		t.Errorf(
			"node count = %d, want %d",
			nodeCount,
			len(participants),
		)
	}
}

func TestBuildContainsOnlyPublicWhitelistedFields(
	t *testing.T,
) {
	files, err := Build(
		[]store.VerifiedOrigin{
			verifiedParticipant(
				t,
				"https://example.com",
				declaration.IdentityAffirmed,
			),
		},
	)
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	forbidden := []string{
		"generated_at",
		"checked_at",
		"observed_at",
		"first_observed_at",
		"latest_observed_at",
		"effective_observed_at",
		"last_claimed_at",
		"lease_expires_at",
		"available_at",
		"lease_generation",
		"worker_id",
		"unavailable",
		"robots_denied",
		"unsupported_version",
		"cross_origin_redirect",
		"resolved_ip",
		"response_body",
		"http_header",
	}

	for _, file := range files {
		for _, field := range forbidden {
			if strings.Contains(
				string(file.Data),
				field,
			) {
				t.Errorf(
					"%s contains private field/value %q",
					file.Path,
					field,
				)
			}
		}
	}
}

func TestBuildMatchesCompleteRegistryGolden(
	t *testing.T,
) {
	participants := []store.VerifiedOrigin{
		verifiedParticipant(
			t,
			"https://example.org",
			declaration.IdentityDeclined,
		),
		verifiedParticipant(
			t,
			"https://example.com",
			declaration.IdentityUndeclared,
		),
		verifiedParticipant(
			t,
			"https://example.net",
			declaration.IdentityAffirmed,
		),
	}

	files, err := Build(participants)
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	if err := testutil.CheckGolden(
		"testdata/golden/registry-tree.golden",
		renderPublicSnapshot(files),
	); err != nil {
		t.Fatalf(
			"registry golden error = %v",
			err,
		)
	}
}

func verifiedParticipant(
	t *testing.T,
	rawURL string,
	identity declaration.Identity,
) store.VerifiedOrigin {
	t.Helper()

	source, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawURL,
			err,
		)
	}

	return store.VerifiedOrigin{
		Origin: source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: identity,
		},
	}
}

func findPublicFile(
	t *testing.T,
	files []File,
	logicalPath string,
) File {
	t.Helper()

	for _, file := range files {
		if file.Path == logicalPath {
			return file
		}
	}

	t.Fatalf(
		"snapshot does not contain %q",
		logicalPath,
	)

	return File{}
}

func onlyNodeFile(
	t *testing.T,
	files []File,
) File {
	t.Helper()

	var nodes []File
	for _, file := range files {
		if strings.HasPrefix(file.Path, "nodes/") {
			nodes = append(nodes, file)
		}
	}

	if len(nodes) != 1 {
		t.Fatalf(
			"node count = %d, want 1",
			len(nodes),
		)
	}

	return nodes[0]
}

func renderPublicSnapshot(files []File) []byte {
	ordered := append([]File(nil), files...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Path < ordered[right].Path
	})

	var output bytes.Buffer
	for index, file := range ordered {
		if index > 0 {
			output.WriteByte('\n')
		}

		fmt.Fprintf(
			&output,
			"=== %s ===\n",
			file.Path,
		)
		output.Write(file.Data)
	}

	return output.Bytes()
}
