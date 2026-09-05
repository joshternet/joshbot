package publicdata

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

var errReaderFileSystem = errors.New(
	"injected reader filesystem failure",
)

func TestReadDirectoryReadsExactDeterministicSnapshot(
	t *testing.T,
) {
	root := t.TempDir()
	abPath := validReaderNodePath("ab")
	cdPath := validReaderNodePath("cd")

	registryData := []byte(
		"{\"format_version\":1,\"nodes\":[]}\r\n",
	)
	abData := []byte(
		"{\"origin\":\"https://example.com\"}\n",
	)
	cdData := []byte{
		0x00,
		0x01,
		0x02,
		'\r',
		'\n',
	}

	writeReaderTestFile(
		t,
		root,
		"registry.json",
		registryData,
	)
	writeReaderTestFile(
		t,
		root,
		cdPath,
		cdData,
	)
	writeReaderTestFile(
		t,
		root,
		abPath,
		abData,
	)

	got, err := ReadDirectory(
		context.Background(),
		root,
	)
	if err != nil {
		t.Fatalf(
			"ReadDirectory() error = %v, want nil",
			err,
		)
	}

	want := []File{
		{
			Path: abPath,
			Data: abData,
		},
		{
			Path: cdPath,
			Data: cdData,
		},
		{
			Path: "registry.json",
			Data: registryData,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf(
			"ReadDirectory() = %#v, want %#v",
			got,
			want,
		)
	}
}

func TestReadDirectoryRequiresRegistry(t *testing.T) {
	root := t.TempDir()
	writeReaderTestFile(
		t,
		root,
		validReaderNodePath("ab"),
		[]byte("{}\n"),
	)

	got, err := ReadDirectory(
		context.Background(),
		root,
	)
	assertReaderError(
		t,
		got,
		err,
		ErrMissingRegistry,
	)
}

func TestReadDirectoryRejectsUnexpectedAndMalformedPaths(
	t *testing.T,
) {
	validHash := "ab" + strings.Repeat("0", 62)

	tests := []struct {
		name string
		path string
	}{
		{
			name: "unexpected root file",
			path: "README.md",
		},
		{
			name: "hidden root file",
			path: ".hidden",
		},
		{
			name: "nested registry",
			path: "nested/registry.json",
		},
		{
			name: "wrong shard length",
			path: "nodes/a/" +
				strings.Repeat("a", 64) +
				".json",
		},
		{
			name: "uppercase shard",
			path: "nodes/AB/" +
				validHash +
				".json",
		},
		{
			name: "wrong hash length",
			path: "nodes/ab/ab.json",
		},
		{
			name: "uppercase hash",
			path: "nodes/ab/AB" +
				strings.Repeat("0", 62) +
				".json",
		},
		{
			name: "hash does not match shard",
			path: "nodes/cd/" +
				validHash +
				".json",
		},
		{
			name: "wrong extension",
			path: "nodes/ab/" +
				validHash +
				".txt",
		},
		{
			name: "extra directory level",
			path: "nodes/ab/extra/" +
				validHash +
				".json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeReaderTestFile(
				t,
				root,
				"registry.json",
				[]byte("{}\n"),
			)
			writeReaderTestFile(
				t,
				root,
				test.path,
				[]byte("unexpected\n"),
			)

			got, err := ReadDirectory(
				context.Background(),
				root,
			)
			assertReaderError(
				t,
				got,
				err,
				ErrInvalidSnapshotEntry,
			)
		})
	}
}

func TestReadDirectoryRejectsSymbolicLinks(
	t *testing.T,
) {
	t.Run("root", func(t *testing.T) {
		target := t.TempDir()
		writeReaderTestFile(
			t,
			target,
			"registry.json",
			[]byte("{}\n"),
		)

		parent := t.TempDir()
		root := filepath.Join(parent, "snapshot")
		if err := os.Symlink(target, root); err != nil {
			t.Skipf(
				"symbolic links are unavailable: %v",
				err,
			)
		}

		got, err := ReadDirectory(
			context.Background(),
			root,
		)
		assertReaderError(
			t,
			got,
			err,
			ErrInvalidSnapshotRoot,
		)
	})

	t.Run("entry", func(t *testing.T) {
		root := t.TempDir()
		writeReaderTestFile(
			t,
			root,
			"registry.json",
			[]byte("{}\n"),
		)

		targetDirectory := t.TempDir()
		target := filepath.Join(
			targetDirectory,
			"outside.json",
		)
		if err := os.WriteFile(
			target,
			[]byte("must not be read\n"),
			0o644,
		); err != nil {
			t.Fatalf(
				"write symbolic-link target: %v",
				err,
			)
		}

		logicalPath := validReaderNodePath("ab")
		link := filepath.Join(
			root,
			filepath.FromSlash(logicalPath),
		)
		if err := os.MkdirAll(
			filepath.Dir(link),
			0o755,
		); err != nil {
			t.Fatalf(
				"create symbolic-link parent: %v",
				err,
			)
		}

		if err := os.Symlink(target, link); err != nil {
			t.Skipf(
				"symbolic links are unavailable: %v",
				err,
			)
		}

		got, err := ReadDirectory(
			context.Background(),
			root,
		)
		assertReaderError(
			t,
			got,
			err,
			ErrInvalidSnapshotEntry,
		)
	})
}

func TestReadDirectoryRejectsNonRegularFiles(
	t *testing.T,
) {
	root := t.TempDir()
	socketPath := filepath.Join(
		root,
		"registry.json",
	)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf(
			"Unix sockets are unavailable: %v",
			err,
		)
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf(
				"close Unix socket: %v",
				closeErr,
			)
		}
	}()

	got, err := ReadDirectory(
		context.Background(),
		root,
	)
	assertReaderError(
		t,
		got,
		err,
		ErrInvalidSnapshotEntry,
	)
}

func TestReadDirectoryValidatesContextAndRoot(
	t *testing.T,
) {
	validRoot := t.TempDir()
	writeReaderTestFile(
		t,
		validRoot,
		"registry.json",
		[]byte("{}\n"),
	)

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	missingRoot := filepath.Join(
		t.TempDir(),
		"missing",
	)
	fileRoot := filepath.Join(
		t.TempDir(),
		"snapshot",
	)
	if err := os.WriteFile(
		fileRoot,
		[]byte("not a directory\n"),
		0o644,
	); err != nil {
		t.Fatalf("write root file: %v", err)
	}

	tests := []struct {
		name      string
		ctx       context.Context
		root      string
		wantError error
	}{
		{
			name:      "nil context",
			ctx:       nil,
			root:      validRoot,
			wantError: ErrInvalidContext,
		},
		{
			name:      "pre-canceled context",
			ctx:       canceledContext,
			root:      validRoot,
			wantError: context.Canceled,
		},
		{
			name:      "empty root",
			ctx:       context.Background(),
			root:      "",
			wantError: ErrInvalidSnapshotRoot,
		},
		{
			name:      "missing root",
			ctx:       context.Background(),
			root:      missingRoot,
			wantError: ErrInvalidSnapshotRoot,
		},
		{
			name:      "root is regular file",
			ctx:       context.Background(),
			root:      fileRoot,
			wantError: ErrInvalidSnapshotRoot,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ReadDirectory(
				test.ctx,
				test.root,
			)
			assertReaderError(
				t,
				got,
				err,
				test.wantError,
			)
		})
	}
}

func TestReadDirectoryUsesFixedConservativeLimits(
	t *testing.T,
) {
	want := readerLimits{
		maxFiles:      4_097,
		maxFileBytes:  8 * 1024 * 1024,
		maxTotalBytes: 32 * 1024 * 1024,
	}

	if defaultReaderLimits != want {
		t.Errorf(
			"defaultReaderLimits = %#v, want %#v",
			defaultReaderLimits,
			want,
		)
	}
}

func TestReadDirectoryEnforcesResourceLimits(
	t *testing.T,
) {
	nodePath := validReaderNodePath("ab")

	tests := []struct {
		name      string
		files     map[string][]byte
		limits    readerLimits
		wantCount int
		wantError error
	}{
		{
			name: "file count exact boundary",
			files: map[string][]byte{
				nodePath:        []byte("n"),
				"registry.json": []byte("r"),
			},
			limits: readerLimits{
				maxFiles:      2,
				maxFileBytes:  1,
				maxTotalBytes: 2,
			},
			wantCount: 2,
		},
		{
			name: "file count exceeds boundary",
			files: map[string][]byte{
				nodePath:        []byte("n"),
				"registry.json": []byte("r"),
			},
			limits: readerLimits{
				maxFiles:      1,
				maxFileBytes:  1,
				maxTotalBytes: 2,
			},
			wantError: ErrSnapshotLimitExceeded,
		},
		{
			name: "individual size exact boundary",
			files: map[string][]byte{
				"registry.json": []byte("1234"),
			},
			limits: readerLimits{
				maxFiles:      1,
				maxFileBytes:  4,
				maxTotalBytes: 4,
			},
			wantCount: 1,
		},
		{
			name: "individual size exceeds boundary",
			files: map[string][]byte{
				"registry.json": []byte("12345"),
			},
			limits: readerLimits{
				maxFiles:      1,
				maxFileBytes:  4,
				maxTotalBytes: 8,
			},
			wantError: ErrSnapshotLimitExceeded,
		},
		{
			name: "total size exact boundary",
			files: map[string][]byte{
				nodePath:        []byte("123"),
				"registry.json": []byte("4567"),
			},
			limits: readerLimits{
				maxFiles:      2,
				maxFileBytes:  4,
				maxTotalBytes: 7,
			},
			wantCount: 2,
		},
		{
			name: "total size exceeds boundary",
			files: map[string][]byte{
				nodePath:        []byte("123"),
				"registry.json": []byte("4567"),
			},
			limits: readerLimits{
				maxFiles:      2,
				maxFileBytes:  4,
				maxTotalBytes: 6,
			},
			wantError: ErrSnapshotLimitExceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readMapSnapshot(
				context.Background(),
				test.files,
				test.limits,
			)

			if test.wantError != nil {
				assertReaderError(
					t,
					got,
					err,
					test.wantError,
				)

				return
			}

			if err != nil {
				t.Fatalf(
					"readDirectory() error = %v, want nil",
					err,
				)
			}

			if len(got) != test.wantCount {
				t.Errorf(
					"readDirectory() file count = %d, want %d",
					len(got),
					test.wantCount,
				)
			}
		})
	}
}

func TestReadDirectoryHonorsCancellationDuringRead(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	directory := newTestSnapshotDirectory(
		map[string][]byte{
			"registry.json": []byte("registry"),
		},
	)
	directory.fileFactory = func(
		file snapshotFile,
	) snapshotFile {
		return &testSnapshotFile{
			snapshotFile: file,
			cancel:       cancel,
		}
	}

	got, err := readDirectory(
		ctx,
		"snapshot",
		readerLimits{
			maxFiles:      1,
			maxFileBytes:  16,
			maxTotalBytes: 16,
		},
		func(string) (snapshotDirectory, error) {
			return directory, nil
		},
	)
	assertReaderError(
		t,
		got,
		err,
		context.Canceled,
	)
}

func TestReadDirectoryRejectsFileChangedBeforeRead(
	t *testing.T,
) {
	directory := newTestSnapshotDirectory(
		map[string][]byte{
			"registry.json": []byte("{}\n"),
		},
	)
	directory.fileFactory = func(
		file snapshotFile,
	) snapshotFile {
		return &testSnapshotFile{
			snapshotFile: file,
			overrideMode: true,
			mode:         fs.ModeDir | 0o755,
		}
	}

	got, err := readDirectory(
		context.Background(),
		"snapshot",
		readerLimits{
			maxFiles:      1,
			maxFileBytes:  16,
			maxTotalBytes: 16,
		},
		func(string) (snapshotDirectory, error) {
			return directory, nil
		},
	)
	assertReaderError(
		t,
		got,
		err,
		ErrInvalidSnapshotEntry,
	)
}

func TestReadDirectoryPropagatesFilesystemFailures(
	t *testing.T,
) {
	tests := []struct {
		name          string
		makeDirectory func() (snapshotDirectory, error)
	}{
		{
			name: "open root",
			makeDirectory: func() (
				snapshotDirectory,
				error,
			) {
				return nil, errReaderFileSystem
			},
		},
		{
			name: "walk root",
			makeDirectory: func() (
				snapshotDirectory,
				error,
			) {
				return &testSnapshotDirectory{
					fileSystem: readerErrorFileSystem{},
				}, nil
			},
		},
		{
			name: "lstat entry",
			makeDirectory: func() (
				snapshotDirectory,
				error,
			) {
				directory := newTestSnapshotDirectory(
					map[string][]byte{
						"registry.json": []byte("{}\n"),
					},
				)
				directory.lstatError =
					errReaderFileSystem

				return directory, nil
			},
		},
		{
			name: "open entry",
			makeDirectory: func() (
				snapshotDirectory,
				error,
			) {
				directory := newTestSnapshotDirectory(
					map[string][]byte{
						"registry.json": []byte("{}\n"),
					},
				)
				directory.openError =
					errReaderFileSystem

				return directory, nil
			},
		},
		{
			name: "stat opened entry",
			makeDirectory: func() (
				snapshotDirectory,
				error,
			) {
				directory := newTestSnapshotDirectory(
					map[string][]byte{
						"registry.json": []byte("{}\n"),
					},
				)
				directory.fileFactory = func(
					file snapshotFile,
				) snapshotFile {
					return &testSnapshotFile{
						snapshotFile: file,
						statError:    errReaderFileSystem,
					}
				}

				return directory, nil
			},
		},
		{
			name: "read entry",
			makeDirectory: func() (
				snapshotDirectory,
				error,
			) {
				directory := newTestSnapshotDirectory(
					map[string][]byte{
						"registry.json": []byte("{}\n"),
					},
				)
				directory.fileFactory = func(
					file snapshotFile,
				) snapshotFile {
					return &testSnapshotFile{
						snapshotFile: file,
						readError:    errReaderFileSystem,
					}
				}

				return directory, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readDirectory(
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
					return test.makeDirectory()
				},
			)
			assertReaderError(
				t,
				got,
				err,
				errReaderFileSystem,
			)
		})
	}
}

func assertReaderError(
	t *testing.T,
	files []File,
	err error,
	want error,
) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf(
			"reader error = %v, want %v",
			err,
			want,
		)
	}

	if files != nil {
		t.Errorf(
			"reader files = %#v, want nil",
			files,
		)
	}
}

func validReaderNodePath(shard string) string {
	return "nodes/" +
		shard +
		"/" +
		shard +
		strings.Repeat("0", 62) +
		".json"
}

func writeReaderTestFile(
	t *testing.T,
	root string,
	logicalPath string,
	data []byte,
) {
	t.Helper()

	destination := filepath.Join(
		root,
		filepath.FromSlash(logicalPath),
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

func readMapSnapshot(
	ctx context.Context,
	files map[string][]byte,
	limits readerLimits,
) ([]File, error) {
	directory := newTestSnapshotDirectory(files)

	return readDirectory(
		ctx,
		"snapshot",
		limits,
		func(string) (snapshotDirectory, error) {
			return directory, nil
		},
	)
}

type testSnapshotDirectory struct {
	fileSystem fs.FS

	lstatError  error
	openError   error
	fileFactory func(snapshotFile) snapshotFile
}

func newTestSnapshotDirectory(
	files map[string][]byte,
) *testSnapshotDirectory {
	fileSystem := make(fstest.MapFS, len(files))

	for logicalPath, data := range files {
		fileSystem[logicalPath] = &fstest.MapFile{
			Data: append([]byte(nil), data...),
			Mode: 0o644,
		}
	}

	return &testSnapshotDirectory{
		fileSystem: fileSystem,
	}
}

func (directory *testSnapshotDirectory) FS() fs.FS {
	return directory.fileSystem
}

func (directory *testSnapshotDirectory) Lstat(
	name string,
) (fs.FileInfo, error) {
	if directory.lstatError != nil {
		return nil, directory.lstatError
	}

	return fs.Stat(directory.fileSystem, name)
}

func (directory *testSnapshotDirectory) Open(
	name string,
) (snapshotFile, error) {
	if directory.openError != nil {
		return nil, directory.openError
	}

	file, err := directory.fileSystem.Open(name)
	if err != nil {
		return nil, err
	}

	if directory.fileFactory != nil {
		return directory.fileFactory(file), nil
	}

	return file, nil
}

func (directory *testSnapshotDirectory) Close() error {
	return nil
}

type readerErrorFileSystem struct{}

func (readerErrorFileSystem) Open(
	string,
) (fs.File, error) {
	return nil, errReaderFileSystem
}

type testSnapshotFile struct {
	snapshotFile

	statError error
	readError error

	overrideMode bool
	mode         fs.FileMode

	cancel   context.CancelFunc
	canceled bool
}

func (file *testSnapshotFile) Stat() (
	fs.FileInfo,
	error,
) {
	if file.statError != nil {
		return nil, file.statError
	}

	info, err := file.snapshotFile.Stat()
	if err != nil {
		return nil, err
	}

	if file.overrideMode {
		return modeFileInfo{
			FileInfo: info,
			mode:     file.mode,
		}, nil
	}

	return info, nil
}

func (file *testSnapshotFile) Read(
	buffer []byte,
) (int, error) {
	if file.readError != nil {
		return 0, file.readError
	}

	if file.cancel != nil && !file.canceled {
		if len(buffer) > 1 {
			buffer = buffer[:1]
		}

		count, err := file.snapshotFile.Read(buffer)
		file.canceled = true
		file.cancel()

		return count, err
	}

	return file.snapshotFile.Read(buffer)
}

type modeFileInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (info modeFileInfo) Mode() fs.FileMode {
	return info.mode
}

func (info modeFileInfo) IsDir() bool {
	return info.mode.IsDir()
}
