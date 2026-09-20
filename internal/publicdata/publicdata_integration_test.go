package publicdata_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/store"
)

func TestPublicDataIntegrationBuildWriteReadRoundTrip(
	t *testing.T,
) {
	participants := []store.VerifiedOrigin{
		publicDataIntegrationParticipant(
			t,
			"https://example.org",
			declaration.IdentityDeclined,
		),
		publicDataIntegrationParticipant(
			t,
			"https://example.com",
			declaration.IdentityAffirmed,
		),
		publicDataIntegrationParticipant(
			t,
			"http://example.net:8080",
			declaration.IdentityUndeclared,
		),
	}

	files, err := publicdata.Build(participants)
	if err != nil {
		t.Fatalf(
			"Build() error = %v, want nil",
			err,
		)
	}

	root := filepath.Join(
		t.TempDir(),
		"registry",
	)

	if err := publicdata.WriteDirectory(
		context.Background(),
		root,
		files,
	); err != nil {
		t.Fatalf(
			"WriteDirectory() error = %v, want nil",
			err,
		)
	}

	readBack, err := publicdata.ReadDirectory(
		context.Background(),
		root,
	)
	if err != nil {
		t.Fatalf(
			"ReadDirectory() error = %v, want nil",
			err,
		)
	}

	if !reflect.DeepEqual(readBack, files) {
		t.Errorf(
			"ReadDirectory() = %#v, want %#v",
			readBack,
			files,
		)
	}

	if err := publicdata.WriteDirectory(
		context.Background(),
		root,
		files,
	); !errors.Is(
		err,
		publicdata.ErrDestinationExists,
	) {
		t.Errorf(
			"second WriteDirectory() error = %v, want %v",
			err,
			publicdata.ErrDestinationExists,
		)
	}
}

func TestPublicDataIntegrationRejectsUnsafeFilesystemState(
	t *testing.T,
) {
	t.Run(
		"missing registry",
		func(t *testing.T) {
			root := t.TempDir()

			files, err := publicdata.ReadDirectory(
				context.Background(),
				root,
			)
			if files != nil {
				t.Errorf(
					"ReadDirectory() files = %#v, want nil",
					files,
				)
			}
			if !errors.Is(
				err,
				publicdata.ErrMissingRegistry,
			) {
				t.Errorf(
					"ReadDirectory() error = %v, want %v",
					err,
					publicdata.ErrMissingRegistry,
				)
			}
		},
	)

	t.Run(
		"symbolic link root",
		func(t *testing.T) {
			target := t.TempDir()

			if err := os.WriteFile(
				filepath.Join(
					target,
					"registry.json",
				),
				[]byte("{}\n"),
				0o644,
			); err != nil {
				t.Fatalf(
					"write registry: %v",
					err,
				)
			}

			parent := t.TempDir()
			root := filepath.Join(
				parent,
				"registry",
			)

			if err := os.Symlink(
				target,
				root,
			); err != nil {
				t.Skipf(
					"symbolic links unavailable: %v",
					err,
				)
			}

			_, err := publicdata.ReadDirectory(
				context.Background(),
				root,
			)
			if !errors.Is(
				err,
				publicdata.ErrInvalidSnapshotRoot,
			) {
				t.Errorf(
					"ReadDirectory() error = %v, want %v",
					err,
					publicdata.ErrInvalidSnapshotRoot,
				)
			}
		},
	)

	t.Run(
		"symbolic link entry",
		func(t *testing.T) {
			files, err := publicdata.Build(
				[]store.VerifiedOrigin{
					publicDataIntegrationParticipant(
						t,
						"https://example.com",
						declaration.IdentityAffirmed,
					),
				},
			)
			if err != nil {
				t.Fatalf(
					"Build() error = %v",
					err,
				)
			}

			root := filepath.Join(
				t.TempDir(),
				"registry",
			)

			if err := publicdata.WriteDirectory(
				context.Background(),
				root,
				files,
			); err != nil {
				t.Fatalf(
					"WriteDirectory() error = %v",
					err,
				)
			}

			var nodePath string
			for _, file := range files {
				if file.Path != "registry.json" {
					nodePath = file.Path
					break
				}
			}
			if nodePath == "" {
				t.Fatal(
					"Build() produced no node path",
				)
			}

			nodeFile := filepath.Join(
				root,
				filepath.FromSlash(nodePath),
			)

			if err := os.Remove(nodeFile); err != nil {
				t.Fatalf(
					"remove node file: %v",
					err,
				)
			}

			external := filepath.Join(
				t.TempDir(),
				"outside.json",
			)

			if err := os.WriteFile(
				external,
				[]byte("must not be read\n"),
				0o644,
			); err != nil {
				t.Fatalf(
					"write outside file: %v",
					err,
				)
			}

			if err := os.Symlink(
				external,
				nodeFile,
			); err != nil {
				t.Skipf(
					"symbolic links unavailable: %v",
					err,
				)
			}

			_, err = publicdata.ReadDirectory(
				context.Background(),
				root,
			)
			if !errors.Is(
				err,
				publicdata.ErrInvalidSnapshotEntry,
			) {
				t.Errorf(
					"ReadDirectory() error = %v, want %v",
					err,
					publicdata.ErrInvalidSnapshotEntry,
				)
			}
		},
	)

	t.Run(
		"unsafe logical output path",
		func(t *testing.T) {
			root := filepath.Join(
				t.TempDir(),
				"registry",
			)

			err := publicdata.WriteDirectory(
				context.Background(),
				root,
				[]publicdata.File{
					{
						Path: "../registry.json",
						Data: []byte("{}\n"),
					},
				},
			)
			if !errors.Is(
				err,
				publicdata.ErrInvalidOutputPath,
			) {
				t.Errorf(
					"WriteDirectory() error = %v, want %v",
					err,
					publicdata.ErrInvalidOutputPath,
				)
			}
		},
	)
}

func TestPublicDataIntegrationHonorsCancellation(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	root := filepath.Join(
		t.TempDir(),
		"registry",
	)

	files := []publicdata.File{
		{
			Path: "registry.json",
			Data: []byte("{}\n"),
		},
	}

	if err := publicdata.WriteDirectory(
		ctx,
		root,
		files,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"WriteDirectory() error = %v, want %v",
			err,
			context.Canceled,
		)
	}

	validRoot := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(
			validRoot,
			"registry.json",
		),
		[]byte("{}\n"),
		0o644,
	); err != nil {
		t.Fatalf(
			"write registry: %v",
			err,
		)
	}

	readFiles, err := publicdata.ReadDirectory(
		ctx,
		validRoot,
	)
	if readFiles != nil {
		t.Errorf(
			"ReadDirectory() files = %#v, want nil",
			readFiles,
		)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"ReadDirectory() error = %v, want %v",
			err,
			context.Canceled,
		)
	}
}

func publicDataIntegrationParticipant(
	t *testing.T,
	rawOrigin string,
	identity declaration.Identity,
) store.VerifiedOrigin {
	t.Helper()

	parsed, err := origin.Parse(rawOrigin)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawOrigin,
			err,
		)
	}

	return store.VerifiedOrigin{
		Origin: parsed,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: identity,
		},
	}
}
