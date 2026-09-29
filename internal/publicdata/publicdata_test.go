package publicdata

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/testutil"
)

func TestBuildRegistryRepresentsAllIdentityStates(
	t *testing.T,
) {
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
			wantJosh: `"josh": true`,
		},
		{
			name:     "declined",
			identity: declaration.IdentityDeclined,
			wantJosh: `"josh": false`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files, err := BuildRegistry(
				[]store.RegistryOrigin{
					registryParticipantForIdentity(
						t,
						"https://example.com",
						test.identity,
					),
				},
			)
			if err != nil {
				t.Fatalf(
					"BuildRegistry() error = %v, want nil",
					err,
				)
			}

			for _, file := range files {
				data := string(file.Data)

				if test.wantNoMember {
					if strings.Contains(
						data,
						`"josh"`,
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

func TestBuildRegistryProducesKnownNodePath(
	t *testing.T,
) {
	files, err := BuildRegistry(
		[]store.RegistryOrigin{
			registryParticipantForIdentity(
				t,
				"https://example.com",
				declaration.IdentityAffirmed,
			),
		},
	)
	if err != nil {
		t.Fatalf(
			"BuildRegistry() error = %v, want nil",
			err,
		)
	}

	const want = "nodes/10/" +
		"100680ad546ce6a577f42f52df33b4cf" +
		"dca756859e664b8d7de329b150d09ce9.json"

	node := onlyNodeFile(t, files)
	if node.Path != want {
		t.Errorf(
			"node path = %q, want %q",
			node.Path,
			want,
		)
	}
}

func TestBuildRegistryKeepsNodePathAcrossParticipationChanges(
	t *testing.T,
) {
	affirmed := registryParticipantForIdentity(
		t,
		"https://example.com",
		declaration.IdentityAffirmed,
	)
	declined := registryParticipantForIdentity(
		t,
		"https://example.com",
		declaration.IdentityDeclined,
	)
	withdrawn := formerRegistryParticipant(
		t,
		"https://example.com",
		declaration.IdentityAffirmed,
	)

	states := []store.RegistryOrigin{
		affirmed,
		declined,
		withdrawn,
	}

	var paths []string
	var data [][]byte

	for _, participant := range states {
		files, err := BuildRegistry(
			[]store.RegistryOrigin{participant},
		)
		if err != nil {
			t.Fatalf(
				"BuildRegistry() error = %v, want nil",
				err,
			)
		}

		node := onlyNodeFile(t, files)

		paths = append(paths, node.Path)
		data = append(
			data,
			append([]byte(nil), node.Data...),
		)
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

	for left := 0; left < len(data); left++ {
		for right := left + 1; right < len(data); right++ {
			if bytes.Equal(data[left], data[right]) {
				t.Errorf(
					"node data for states %d and %d is identical",
					left,
					right,
				)
			}
		}
	}
}

func TestBuildRegistryUsesSafeHashedNodePaths(
	t *testing.T,
) {
	participants := []store.RegistryOrigin{
		registryParticipantForIdentity(
			t,
			"https://[2001:db8::1]",
			declaration.IdentityUndeclared,
		),
		registryParticipantForIdentity(
			t,
			"https://example.com:8443",
			declaration.IdentityAffirmed,
		),
		registryParticipantForIdentity(
			t,
			"https://xn--bcher-kva.example",
			declaration.IdentityDeclined,
		),
	}

	files, err := BuildRegistry(participants)
	if err != nil {
		t.Fatalf(
			"BuildRegistry() error = %v, want nil",
			err,
		)
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
			if strings.Contains(
				file.Path,
				forbidden,
			) {
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

func TestBuildRegistryMatchesCompleteRegistryGolden(
	t *testing.T,
) {
	participants := []store.RegistryOrigin{
		registryParticipantForIdentity(
			t,
			"https://example.org",
			declaration.IdentityDeclined,
		),
		registryParticipantForIdentity(
			t,
			"https://example.com",
			declaration.IdentityUndeclared,
		),
		registryParticipantForIdentity(
			t,
			"https://example.net",
			declaration.IdentityAffirmed,
		),
		formerRegistryParticipant(
			t,
			"https://example.edu",
			declaration.IdentityAffirmed,
		),
	}

	files, err := BuildRegistry(participants)
	if err != nil {
		t.Fatalf(
			"BuildRegistry() error = %v, want nil",
			err,
		)
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

func registryParticipantForIdentity(
	t *testing.T,
	rawURL string,
	identity declaration.Identity,
) store.RegistryOrigin {
	t.Helper()

	source, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawURL,
			err,
		)
	}

	checkedAt := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	declarationState := declaration.Declaration{
		Version:  1,
		Identity: identity,
	}

	return store.RegistryOrigin{
		Origin:                        source,
		FirstParticipatedAt:           checkedAt,
		InitialDeclaration:            declarationState,
		LatestDeclarationCheckAt:      checkedAt,
		LatestDeclarationCheckOutcome: declaration.OutcomeValid,
		CurrentDeclaration:            &declarationState,
	}
}

func formerRegistryParticipant(
	t *testing.T,
	rawURL string,
	identity declaration.Identity,
) store.RegistryOrigin {
	t.Helper()

	participant := registryParticipantForIdentity(
		t,
		rawURL,
		identity,
	)

	participant.LatestDeclarationCheckAt =
		participant.FirstParticipatedAt.Add(time.Hour)
	participant.LatestDeclarationCheckOutcome =
		declaration.OutcomeAbsent
	participant.CurrentDeclaration = nil

	return participant
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
		if strings.HasPrefix(
			file.Path,
			"nodes/",
		) {
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

func renderPublicSnapshot(
	files []File,
) []byte {
	ordered := append(
		[]File(nil),
		files...,
	)

	sort.Slice(
		ordered,
		func(left, right int) bool {
			return ordered[left].Path <
				ordered[right].Path
		},
	)

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
