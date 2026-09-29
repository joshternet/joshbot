package publicdata

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

func TestBuildRegistryProducesEmptyRegistry(t *testing.T) {
	got, err := BuildRegistry(nil)
	if err != nil {
		t.Fatalf("BuildRegistry() error = %v, want nil", err)
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
			"BuildRegistry() = %#v, want %#v",
			got,
			want,
		)
	}
}

func TestBuildRegistryPublishesParticipationHistory(
	t *testing.T,
) {
	location := time.FixedZone(
		"test",
		-7*60*60,
	)
	currentFirst := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		location,
	)
	currentLatest := currentFirst.Add(
		24 * time.Hour,
	)
	formerFirst := time.Date(
		2026,
		time.August,
		15,
		10,
		0,
		0,
		0,
		time.UTC,
	)
	formerLatest := time.Date(
		2026,
		time.September,
		3,
		10,
		0,
		0,
		0,
		time.UTC,
	)

	currentDeclaration := declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}

	participants := []store.RegistryOrigin{
		registryParticipant(
			t,
			"https://example.org",
			formerFirst,
			formerLatest,
			declaration.IdentityAffirmed,
			declaration.OutcomeAbsent,
			nil,
		),
		registryParticipant(
			t,
			"https://example.com",
			currentFirst,
			currentLatest,
			declaration.IdentityDeclined,
			declaration.OutcomeUnavailable,
			&currentDeclaration,
		),
	}

	files, err := BuildRegistry(participants)
	if err != nil {
		t.Fatalf(
			"BuildRegistry() error = %v, want nil",
			err,
		)
	}

	currentPath := pathForOrigin(
		"https://example.com",
	)
	formerPath := pathForOrigin(
		"https://example.org",
	)

	currentNode := findPublicFile(
		t,
		files,
		currentPath,
	)
	wantCurrentNode := "{\n" +
		"  \"format_version\": 1,\n" +
		"  \"origin\": \"https://example.com\",\n" +
		"  \"first_participated_at\": \"2026-09-01T19:00:00Z\",\n" +
		"  \"initial_declaration\": {\n" +
		"    \"version\": 1,\n" +
		"    \"josh\": false\n" +
		"  },\n" +
		"  \"latest_declaration_check_at\": \"2026-09-02T19:00:00Z\",\n" +
		"  \"latest_declaration_check_outcome\": \"unavailable\",\n" +
		"  \"declaration\": {\n" +
		"    \"version\": 1,\n" +
		"    \"josh\": true\n" +
		"  }\n" +
		"}\n"

	if string(currentNode.Data) != wantCurrentNode {
		t.Errorf(
			"current node data =\n%s\nwant:\n%s",
			currentNode.Data,
			wantCurrentNode,
		)
	}

	formerNode := findPublicFile(
		t,
		files,
		formerPath,
	)
	wantFormerNode := "{\n" +
		"  \"format_version\": 1,\n" +
		"  \"origin\": \"https://example.org\",\n" +
		"  \"first_participated_at\": \"2026-08-15T10:00:00Z\",\n" +
		"  \"initial_declaration\": {\n" +
		"    \"version\": 1,\n" +
		"    \"josh\": true\n" +
		"  },\n" +
		"  \"latest_declaration_check_at\": \"2026-09-03T10:00:00Z\",\n" +
		"  \"latest_declaration_check_outcome\": \"absent\"\n" +
		"}\n"

	if string(formerNode.Data) != wantFormerNode {
		t.Errorf(
			"former node data =\n%s\nwant:\n%s",
			formerNode.Data,
			wantFormerNode,
		)
	}

	registry := findPublicFile(
		t,
		files,
		"registry.json",
	)
	wantRegistry := "{\n" +
		"  \"format_version\": 1,\n" +
		"  \"nodes\": [\n" +
		"    {\n" +
		"      \"origin\": \"https://example.com\",\n" +
		"      \"path\": \"" + currentPath + "\",\n" +
		"      \"first_participated_at\": \"2026-09-01T19:00:00Z\",\n" +
		"      \"initial_declaration\": {\n" +
		"        \"version\": 1,\n" +
		"        \"josh\": false\n" +
		"      },\n" +
		"      \"latest_declaration_check_at\": \"2026-09-02T19:00:00Z\",\n" +
		"      \"latest_declaration_check_outcome\": \"unavailable\",\n" +
		"      \"declaration\": {\n" +
		"        \"version\": 1,\n" +
		"        \"josh\": true\n" +
		"      }\n" +
		"    },\n" +
		"    {\n" +
		"      \"origin\": \"https://example.org\",\n" +
		"      \"path\": \"" + formerPath + "\",\n" +
		"      \"first_participated_at\": \"2026-08-15T10:00:00Z\",\n" +
		"      \"initial_declaration\": {\n" +
		"        \"version\": 1,\n" +
		"        \"josh\": true\n" +
		"      },\n" +
		"      \"latest_declaration_check_at\": \"2026-09-03T10:00:00Z\",\n" +
		"      \"latest_declaration_check_outcome\": \"absent\"\n" +
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

	for index := 1; index < len(files); index++ {
		if files[index-1].Path >= files[index].Path {
			t.Errorf(
				"file order %q then %q is not ascending",
				files[index-1].Path,
				files[index].Path,
			)
		}
	}

	if participants[0].Origin.String() !=
		"https://example.org" {
		t.Errorf(
			"BuildRegistry() mutated input order",
		)
	}
}

func TestBuildRegistryRepresentsEveryCheckOutcome(
	t *testing.T,
) {
	first := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	latest := first.Add(time.Hour)
	current := declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}

	tests := []struct {
		name        string
		outcome     declaration.Outcome
		wantText    string
		declaration *declaration.Declaration
	}{
		{
			name:        "valid",
			outcome:     declaration.OutcomeValid,
			wantText:    "valid",
			declaration: &current,
		},
		{
			name:     "absent",
			outcome:  declaration.OutcomeAbsent,
			wantText: "absent",
		},
		{
			name:     "invalid",
			outcome:  declaration.OutcomeInvalid,
			wantText: "invalid",
		},
		{
			name: "unsupported version",
			outcome: declaration.
				OutcomeUnsupportedVersion,
			wantText: "unsupported_version",
		},
		{
			name:        "unavailable with current declaration",
			outcome:     declaration.OutcomeUnavailable,
			wantText:    "unavailable",
			declaration: &current,
		},
		{
			name:     "robots denied without current declaration",
			outcome:  declaration.OutcomeRobotsDenied,
			wantText: "robots_denied",
		},
		{
			name: "cross-origin redirect",
			outcome: declaration.
				OutcomeCrossOriginRedirect,
			wantText: "cross_origin_redirect",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				participant := registryParticipant(
					t,
					"https://example.com",
					first,
					latest,
					declaration.IdentityUndeclared,
					test.outcome,
					test.declaration,
				)

				files, err := BuildRegistry(
					[]store.RegistryOrigin{
						participant,
					},
				)
				if err != nil {
					t.Fatalf(
						"BuildRegistry() error = %v, want nil",
						err,
					)
				}

				registry := string(
					findPublicFile(
						t,
						files,
						"registry.json",
					).Data,
				)

				if !strings.Contains(
					registry,
					"\"latest_declaration_check_outcome\": \""+
						test.wantText+
						"\"",
				) {
					t.Errorf(
						"registry does not contain outcome %q",
						test.wantText,
					)
				}
			},
		)
	}
}

func TestBuildRegistryRejectsInvalidInput(t *testing.T) {
	first := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	latest := first.Add(time.Hour)
	current := declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}
	valid := registryParticipant(
		t,
		"https://example.com",
		first,
		latest,
		declaration.IdentityAffirmed,
		declaration.OutcomeValid,
		&current,
	)

	tests := []struct {
		name      string
		mutate    func(*store.RegistryOrigin)
		wantError error
	}{
		{
			name: "zero origin",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.Origin = origin.Origin{}
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "zero first participated time",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.FirstParticipatedAt = time.Time{}
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "zero latest check time",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.LatestDeclarationCheckAt = time.Time{}
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "participation after latest check",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.FirstParticipatedAt =
					participant.LatestDeclarationCheckAt.Add(
						time.Second,
					)
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "invalid initial version",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.InitialDeclaration.Version = 2
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "invalid initial identity",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.InitialDeclaration.Identity =
					declaration.Identity(255)
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "invalid current version",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				invalid := *participant.CurrentDeclaration
				invalid.Version = 2
				participant.CurrentDeclaration = &invalid
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "invalid current identity",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				invalid := *participant.CurrentDeclaration
				invalid.Identity = declaration.Identity(255)
				participant.CurrentDeclaration = &invalid
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "valid outcome without declaration",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.CurrentDeclaration = nil
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "absent outcome with declaration",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.LatestDeclarationCheckOutcome =
					declaration.OutcomeAbsent
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "invalid outcome with declaration",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.LatestDeclarationCheckOutcome =
					declaration.OutcomeInvalid
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "unsupported outcome with declaration",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.LatestDeclarationCheckOutcome =
					declaration.OutcomeUnsupportedVersion
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "cross-origin outcome with declaration",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.LatestDeclarationCheckOutcome =
					declaration.OutcomeCrossOriginRedirect
			},
			wantError: ErrInvalidRegistryOrigin,
		},
		{
			name: "unknown outcome",
			mutate: func(
				participant *store.RegistryOrigin,
			) {
				participant.LatestDeclarationCheckOutcome =
					declaration.Outcome(255)
			},
			wantError: ErrInvalidRegistryOrigin,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				participant := valid
				test.mutate(&participant)

				got, err := BuildRegistry(
					[]store.RegistryOrigin{
						participant,
					},
				)

				if !errors.Is(
					err,
					test.wantError,
				) {
					t.Errorf(
						"BuildRegistry() error = %v, want %v",
						err,
						test.wantError,
					)
				}

				if got != nil {
					t.Errorf(
						"BuildRegistry() = %#v, want nil",
						got,
					)
				}
			},
		)
	}

	got, err := BuildRegistry(
		[]store.RegistryOrigin{
			valid,
			valid,
		},
	)
	if !errors.Is(err, ErrDuplicateOrigin) {
		t.Errorf(
			"BuildRegistry(duplicate) error = %v, want %v",
			err,
			ErrDuplicateOrigin,
		)
	}
	if got != nil {
		t.Errorf(
			"BuildRegistry(duplicate) = %#v, want nil",
			got,
		)
	}
}

func TestBuildRegistryContainsOnlyWhitelistedMetadata(
	t *testing.T,
) {
	first := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	current := declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}

	files, err := BuildRegistry(
		[]store.RegistryOrigin{
			registryParticipant(
				t,
				"https://example.com",
				first,
				first.Add(time.Hour),
				declaration.IdentityAffirmed,
				declaration.OutcomeUnavailable,
				&current,
			),
		},
	)
	if err != nil {
		t.Fatalf(
			"BuildRegistry() error = %v, want nil",
			err,
		)
	}

	forbidden := []string{
		"first_observed_at",
		"participation_status",
		"is_active",
		"founder",
		"ever_participated",
		"generated_at",
		"observed_at",
		"latest_observed_at",
		"effective_observed_at",
		"last_claimed_at",
		"lease_expires_at",
		"available_at",
		"lease_generation",
		"worker_id",
		"reprobe",
		"miss_count",
		"resolved_ip",
		"response_body",
		"http_header",
	}

	for _, file := range files {
		data := string(file.Data)

		for _, field := range forbidden {
			if strings.Contains(data, field) {
				t.Errorf(
					"%s contains non-public field/value %q",
					file.Path,
					field,
				)
			}
		}
	}
}

func TestBuildRegistryKeepsFormatVersionOne(
	t *testing.T,
) {
	if FormatVersion != 1 {
		t.Fatalf(
			"FormatVersion = %d, want 1 for additive registry fields",
			FormatVersion,
		)
	}
}

func registryParticipant(
	t *testing.T,
	rawURL string,
	firstParticipatedAt time.Time,
	latestCheckAt time.Time,
	initialIdentity declaration.Identity,
	latestOutcome declaration.Outcome,
	currentDeclaration *declaration.Declaration,
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

	return store.RegistryOrigin{
		Origin:              source,
		FirstParticipatedAt: firstParticipatedAt,
		InitialDeclaration: declaration.Declaration{
			Version:  1,
			Identity: initialIdentity,
		},
		LatestDeclarationCheckAt:      latestCheckAt,
		LatestDeclarationCheckOutcome: latestOutcome,
		CurrentDeclaration:            currentDeclaration,
	}
}
