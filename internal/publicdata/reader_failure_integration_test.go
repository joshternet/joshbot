package publicdata

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

var errPublicDataReaderFailureIntegration = errors.New(
	"integration publicdata reader filesystem failure",
)

func TestPublicDataReaderFailureIntegrationRejectsInvalidBuildInput(
	t *testing.T,
) {
	validOrigin := publicDataReaderFailureIntegrationOrigin(
		t,
		"https://example.com",
	)
	valid := store.VerifiedOrigin{
		Origin: validOrigin,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}

	tests := []struct {
		name  string
		input []store.VerifiedOrigin
		want  error
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
			want: ErrInvalidVerifiedOrigin,
		},
		{
			name: "bad version",
			input: []store.VerifiedOrigin{
				{
					Origin: validOrigin,
					Declaration: declaration.Declaration{
						Version:  2,
						Identity: declaration.IdentityAffirmed,
					},
				},
			},
			want: ErrInvalidVerifiedOrigin,
		},
		{
			name: "bad identity",
			input: []store.VerifiedOrigin{
				{
					Origin: validOrigin,
					Declaration: declaration.Declaration{
						Version:  1,
						Identity: declaration.Identity(255),
					},
				},
			},
			want: ErrInvalidVerifiedOrigin,
		},
		{
			name:  "duplicate origin",
			input: []store.VerifiedOrigin{valid, valid},
			want:  ErrDuplicateOrigin,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files, err := Build(test.input)
			if !errors.Is(err, test.want) {
				t.Fatalf(
					"Build() error = %v, want %v",
					err,
					test.want,
				)
			}

			if files != nil {
				t.Errorf(
					"Build() files = %#v, want nil",
					files,
				)
			}
		})
	}
}

func TestPublicDataReaderFailureIntegrationValidatesContextAndRoot(
	t *testing.T,
) {
	validRoot := t.TempDir()
	publicDataReaderFailureIntegrationWriteFile(
		t,
		validRoot,
		"registry.json",
		[]byte("{}\n"),
	)

	if files, err := ReadDirectory(
		nil,
		validRoot,
	); !errors.Is(err, ErrInvalidContext) || files != nil {
		t.Errorf(
			"ReadDirectory(nil) = %#v, %v, want nil, %v",
			files,
			err,
			ErrInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if files, err := ReadDirectory(
		ctx,
		validRoot,
	); !errors.Is(err, context.Canceled) || files != nil {
		t.Errorf(
			"ReadDirectory(canceled) = %#v, %v, want nil, context.Canceled",
			files,
			err,
		)
	}

	if files, err := ReadDirectory(
		context.Background(),
		"",
	); !errors.Is(err, ErrInvalidSnapshotRoot) || files != nil {
		t.Errorf(
			"ReadDirectory(empty root) = %#v, %v",
			files,
			err,
		)
	}

	missing := filepath.Join(
		t.TempDir(),
		"missing",
	)

	if files, err := ReadDirectory(
		context.Background(),
		missing,
	); !errors.Is(err, ErrInvalidSnapshotRoot) || files != nil {
		t.Errorf(
			"ReadDirectory(missing root) = %#v, %v",
			files,
			err,
		)
	}

	fileRoot := filepath.Join(
		t.TempDir(),
		"snapshot",
	)
	if err := os.WriteFile(
		fileRoot,
		[]byte("file\n"),
		0o644,
	); err != nil {
		t.Fatalf(
			"write root file: %v",
			err,
		)
	}

	if files, err := ReadDirectory(
		context.Background(),
		fileRoot,
	); !errors.Is(err, ErrInvalidSnapshotRoot) || files != nil {
		t.Errorf(
			"ReadDirectory(file root) = %#v, %v",
			files,
			err,
		)
	}

	t.Run("symbolic link root", func(t *testing.T) {
		target := t.TempDir()
		publicDataReaderFailureIntegrationWriteFile(
			t,
			target,
			"registry.json",
			[]byte("{}\n"),
		)

		parent := t.TempDir()
		root := filepath.Join(
			parent,
			"snapshot",
		)
		if err := os.Symlink(target, root); err != nil {
			t.Skipf(
				"symbolic links unavailable: %v",
				err,
			)
		}

		files, err := ReadDirectory(
			context.Background(),
			root,
		)
		if !errors.Is(err, ErrInvalidSnapshotRoot) ||
			files != nil {
			t.Errorf(
				"ReadDirectory(symlink root) = %#v, %v",
				files,
				err,
			)
		}
	})
}

func TestPublicDataReaderFailureIntegrationRejectsEntriesAndLimits(
	t *testing.T,
) {
	t.Run("unexpected directory", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					"registry.json":   []byte("{}\n"),
					"unexpected/file": []byte("x"),
				},
			)

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      4,
				maxFileBytes:  16,
				maxTotalBytes: 64,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrInvalidSnapshotEntry,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want invalid snapshot entry",
				files,
				err,
			)
		}
	})

	t.Run("symbolic link entry", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					"registry.json": []byte("{}\n"),
				},
			)

		mapFS := directory.fileSystem.(fstest.MapFS)
		mapFS["registry.json"].Mode =
			fs.ModeSymlink | 0o777

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      1,
				maxFileBytes:  16,
				maxTotalBytes: 16,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrInvalidSnapshotEntry,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want invalid snapshot entry",
				files,
				err,
			)
		}
	})

	t.Run("non regular lstat entry", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					"registry.json": []byte("{}\n"),
				},
			)

		info, err := fs.Stat(
			directory.fileSystem,
			"registry.json",
		)
		if err != nil {
			t.Fatalf(
				"stat registry: %v",
				err,
			)
		}

		directory.lstatInfo =
			publicDataReaderFailureIntegrationFileInfo{
				FileInfo: info,
				mode:     fs.ModeNamedPipe | 0o644,
			}

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      1,
				maxFileBytes:  16,
				maxTotalBytes: 16,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrInvalidSnapshotEntry,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want invalid snapshot entry",
				files,
				err,
			)
		}
	})

	t.Run("file count limit", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					"registry.json": []byte("{}\n"),
				},
			)

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      0,
				maxFileBytes:  16,
				maxTotalBytes: 16,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrSnapshotLimitExceeded,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want snapshot limit",
				files,
				err,
			)
		}
	})

	t.Run("file size limit", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					"registry.json": []byte("12345"),
				},
			)

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      1,
				maxFileBytes:  4,
				maxTotalBytes: 8,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrSnapshotLimitExceeded,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want snapshot limit",
				files,
				err,
			)
		}
	})

	t.Run("total size limit", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					publicDataReaderFailureIntegrationNodePath(
						"ab",
					): []byte("123"),
					"registry.json": []byte("4567"),
				},
			)

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      2,
				maxFileBytes:  4,
				maxTotalBytes: 6,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrSnapshotLimitExceeded,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want snapshot limit",
				files,
				err,
			)
		}
	})

	t.Run("missing registry", func(t *testing.T) {
		directory :=
			publicDataReaderFailureIntegrationDirectoryFromMap(
				map[string][]byte{
					publicDataReaderFailureIntegrationNodePath(
						"ab",
					): []byte("{}\n"),
				},
			)

		files, err := readDirectory(
			context.Background(),
			"snapshot",
			readerLimits{
				maxFiles:      1,
				maxFileBytes:  16,
				maxTotalBytes: 16,
			},
			func(string) (
				snapshotDirectory,
				error,
			) {
				return directory, nil
			},
		)

		if !errors.Is(
			err,
			ErrMissingRegistry,
		) || files != nil {
			t.Errorf(
				"readDirectory() = %#v, %v, want missing registry",
				files,
				err,
			)
		}
	})
}

func TestPublicDataReaderFailureIntegrationPropagatesIOFailures(
	t *testing.T,
) {
	limits := readerLimits{
		maxFiles:      1,
		maxFileBytes:  16,
		maxTotalBytes: 16,
	}

	tests := []struct {
		name string
		make func() (snapshotDirectory, error)
		want error
	}{
		{
			name: "open root",
			make: func() (
				snapshotDirectory,
				error,
			) {
				return nil,
					errPublicDataReaderFailureIntegration
			},
			want: ErrInvalidSnapshotRoot,
		},
		{
			name: "walk root",
			make: func() (
				snapshotDirectory,
				error,
			) {
				return &publicDataReaderFailureIntegrationDirectory{
					fileSystem: publicDataReaderFailureIntegrationErrorFS{},
				}, nil
			},
			want: errPublicDataReaderFailureIntegration,
		},
		{
			name: "lstat entry",
			make: func() (
				snapshotDirectory,
				error,
			) {
				directory :=
					publicDataReaderFailureIntegrationDirectoryFromMap(
						map[string][]byte{
							"registry.json": []byte("{}\n"),
						},
					)
				directory.lstatError =
					errPublicDataReaderFailureIntegration

				return directory, nil
			},
			want: errPublicDataReaderFailureIntegration,
		},
		{
			name: "open entry",
			make: func() (
				snapshotDirectory,
				error,
			) {
				directory :=
					publicDataReaderFailureIntegrationDirectoryFromMap(
						map[string][]byte{
							"registry.json": []byte("{}\n"),
						},
					)
				directory.openError =
					errPublicDataReaderFailureIntegration

				return directory, nil
			},
			want: errPublicDataReaderFailureIntegration,
		},
		{
			name: "stat opened entry",
			make: func() (
				snapshotDirectory,
				error,
			) {
				directory :=
					publicDataReaderFailureIntegrationDirectoryFromMap(
						map[string][]byte{
							"registry.json": []byte("{}\n"),
						},
					)
				directory.fileFactory = func(
					file snapshotFile,
				) snapshotFile {
					return &publicDataReaderFailureIntegrationFile{
						snapshotFile: file,
						statError:    errPublicDataReaderFailureIntegration,
					}
				}

				return directory, nil
			},
			want: errPublicDataReaderFailureIntegration,
		},
		{
			name: "read entry",
			make: func() (
				snapshotDirectory,
				error,
			) {
				directory :=
					publicDataReaderFailureIntegrationDirectoryFromMap(
						map[string][]byte{
							"registry.json": []byte("{}\n"),
						},
					)
				directory.fileFactory = func(
					file snapshotFile,
				) snapshotFile {
					return &publicDataReaderFailureIntegrationFile{
						snapshotFile: file,
						readError:    errPublicDataReaderFailureIntegration,
					}
				}

				return directory, nil
			},
			want: errPublicDataReaderFailureIntegration,
		},
		{
			name: "entry changes before read",
			make: func() (
				snapshotDirectory,
				error,
			) {
				directory :=
					publicDataReaderFailureIntegrationDirectoryFromMap(
						map[string][]byte{
							"registry.json": []byte("{}\n"),
						},
					)
				directory.fileFactory = func(
					file snapshotFile,
				) snapshotFile {
					return &publicDataReaderFailureIntegrationFile{
						snapshotFile: file,
						overrideMode: true,
						mode: fs.ModeDir |
							0o755,
					}
				}

				return directory, nil
			},
			want: ErrInvalidSnapshotEntry,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files, err := readDirectory(
				context.Background(),
				"snapshot",
				limits,
				func(string) (
					snapshotDirectory,
					error,
				) {
					return test.make()
				},
			)

			if !errors.Is(err, test.want) ||
				files != nil {
				t.Errorf(
					"readDirectory() = %#v, %v, want nil, %v",
					files,
					err,
					test.want,
				)
			}
		})
	}
}

func TestPublicDataReaderFailureIntegrationHonorsCancellationDuringRead(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	directory :=
		publicDataReaderFailureIntegrationDirectoryFromMap(
			map[string][]byte{
				"registry.json": []byte("registry"),
			},
		)

	directory.fileFactory = func(
		file snapshotFile,
	) snapshotFile {
		return &publicDataReaderFailureIntegrationFile{
			snapshotFile: file,
			cancel:       cancel,
		}
	}

	files, err := readDirectory(
		ctx,
		"snapshot",
		readerLimits{
			maxFiles:      1,
			maxFileBytes:  16,
			maxTotalBytes: 16,
		},
		func(string) (
			snapshotDirectory,
			error,
		) {
			return directory, nil
		},
	)

	if !errors.Is(err, context.Canceled) ||
		files != nil {
		t.Errorf(
			"readDirectory() = %#v, %v, want nil, context.Canceled",
			files,
			err,
		)
	}
}

type publicDataReaderFailureIntegrationDirectory struct {
	fileSystem fs.FS

	lstatError error
	lstatInfo  fs.FileInfo
	openError  error

	fileFactory func(snapshotFile) snapshotFile
}

func publicDataReaderFailureIntegrationDirectoryFromMap(
	files map[string][]byte,
) *publicDataReaderFailureIntegrationDirectory {
	fileSystem := make(
		fstest.MapFS,
		len(files),
	)

	for logicalPath, data := range files {
		fileSystem[logicalPath] = &fstest.MapFile{
			Data: append(
				[]byte(nil),
				data...,
			),
			Mode: 0o644,
		}
	}

	return &publicDataReaderFailureIntegrationDirectory{
		fileSystem: fileSystem,
	}
}

func (directory *publicDataReaderFailureIntegrationDirectory) FS() fs.FS {
	return directory.fileSystem
}

func (directory *publicDataReaderFailureIntegrationDirectory) Lstat(
	name string,
) (fs.FileInfo, error) {
	if directory.lstatError != nil {
		return nil,
			directory.lstatError
	}

	if directory.lstatInfo != nil {
		return directory.lstatInfo,
			nil
	}

	return fs.Stat(
		directory.fileSystem,
		name,
	)
}

func (directory *publicDataReaderFailureIntegrationDirectory) Open(
	name string,
) (snapshotFile, error) {
	if directory.openError != nil {
		return nil,
			directory.openError
	}

	file, err := directory.fileSystem.Open(
		name,
	)
	if err != nil {
		return nil,
			err
	}

	if directory.fileFactory != nil {
		return directory.fileFactory(file),
			nil
	}

	return file,
		nil
}

func (*publicDataReaderFailureIntegrationDirectory) Close() error {
	return nil
}

type publicDataReaderFailureIntegrationErrorFS struct{}

func (publicDataReaderFailureIntegrationErrorFS) Open(
	string,
) (fs.File, error) {
	return nil,
		errPublicDataReaderFailureIntegration
}

type publicDataReaderFailureIntegrationFile struct {
	snapshotFile

	statError error
	readError error

	overrideMode bool
	mode         fs.FileMode

	cancel   context.CancelFunc
	canceled bool
}

func (file *publicDataReaderFailureIntegrationFile) Stat() (
	fs.FileInfo,
	error,
) {
	if file.statError != nil {
		return nil,
			file.statError
	}

	info, err := file.snapshotFile.Stat()
	if err != nil {
		return nil,
			err
	}

	if file.overrideMode {
		return publicDataReaderFailureIntegrationFileInfo{
			FileInfo: info,
			mode:     file.mode,
		}, nil
	}

	return info,
		nil
}

func (file *publicDataReaderFailureIntegrationFile) Read(
	buffer []byte,
) (int, error) {
	if file.readError != nil {
		return 0,
			file.readError
	}

	if file.cancel != nil &&
		!file.canceled {
		if len(buffer) > 1 {
			buffer = buffer[:1]
		}

		count, err :=
			file.snapshotFile.Read(buffer)

		file.canceled = true
		file.cancel()

		return count,
			err
	}

	return file.snapshotFile.Read(buffer)
}

type publicDataReaderFailureIntegrationFileInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (info publicDataReaderFailureIntegrationFileInfo) Mode() fs.FileMode {
	return info.mode
}

func (info publicDataReaderFailureIntegrationFileInfo) IsDir() bool {
	return info.mode.IsDir()
}

func publicDataReaderFailureIntegrationNodePath(
	shard string,
) string {
	return "nodes/" +
		shard +
		"/" +
		shard +
		strings.Repeat(
			"0",
			62,
		) +
		".json"
}

func publicDataReaderFailureIntegrationWriteFile(
	t *testing.T,
	root string,
	logicalPath string,
	data []byte,
) {
	t.Helper()

	destination := filepath.Join(
		root,
		filepath.FromSlash(
			logicalPath,
		),
	)

	if err := os.MkdirAll(
		filepath.Dir(destination),
		0o755,
	); err != nil {
		t.Fatalf(
			"create parent for %q: %v",
			logicalPath,
			err,
		)
	}

	if err := os.WriteFile(
		destination,
		data,
		0o644,
	); err != nil {
		t.Fatalf(
			"write %q: %v",
			logicalPath,
			err,
		)
	}
}

func publicDataReaderFailureIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return parsed
}
