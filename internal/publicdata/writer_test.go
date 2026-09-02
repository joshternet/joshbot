package publicdata

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/store"
)

var errInjectedFileSystem = errors.New(
	"injected filesystem failure",
)

func TestWriteDirectoryProducesCompleteTree(
	t *testing.T,
) {
	files, err := Build(
		[]store.VerifiedOrigin{
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
		},
	)
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	parent := t.TempDir()
	root := filepath.Join(parent, "export")

	if _, err := os.Stat(root); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf(
			"destination before write error = %v, want os.ErrNotExist",
			err,
		)
	}

	if err := WriteDirectory(
		context.Background(),
		root,
		files,
	); err != nil {
		t.Fatalf(
			"WriteDirectory() error = %v, want nil",
			err,
		)
	}

	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}

	if !rootInfo.IsDir() {
		t.Fatal("destination is not a directory")
	}

	if rootInfo.Mode().Perm()&0o111 == 0 {
		t.Errorf(
			"destination mode = %o, want directory execute bits",
			rootInfo.Mode().Perm(),
		)
	}

	var actualPaths []string
	err = filepath.WalkDir(
		root,
		func(
			currentPath string,
			entry fs.DirEntry,
			walkError error,
		) error {
			if walkError != nil {
				return walkError
			}

			if entry.IsDir() {
				return nil
			}

			relative, relativeError := filepath.Rel(
				root,
				currentPath,
			)
			if relativeError != nil {
				return relativeError
			}

			actualPaths = append(
				actualPaths,
				filepath.ToSlash(relative),
			)

			info, infoError := entry.Info()
			if infoError != nil {
				return infoError
			}

			if info.Mode().Perm()&0o111 != 0 {
				t.Errorf(
					"%s mode = %o, want non-executable",
					relative,
					info.Mode().Perm(),
				)
			}

			return nil
		},
	)
	if err != nil {
		t.Fatalf("walk destination: %v", err)
	}

	var wantPaths []string
	for _, file := range files {
		wantPaths = append(wantPaths, file.Path)

		got, readErr := os.ReadFile(
			filepath.Join(
				root,
				filepath.FromSlash(file.Path),
			),
		)
		if readErr != nil {
			t.Fatalf(
				"read %q: %v",
				file.Path,
				readErr,
			)
		}

		if !reflect.DeepEqual(got, file.Data) {
			t.Errorf(
				"%s data = %q, want %q",
				file.Path,
				got,
				file.Data,
			)
		}
	}

	sort.Strings(actualPaths)
	sort.Strings(wantPaths)
	if !reflect.DeepEqual(actualPaths, wantPaths) {
		t.Errorf(
			"written paths = %#v, want %#v",
			actualPaths,
			wantPaths,
		)
	}

	assertNoWriterStaging(t, parent, "export")
}

func TestWriteDirectoryRejectsExistingDestination(
	t *testing.T,
) {
	parent := t.TempDir()
	root := filepath.Join(parent, "export")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create destination: %v", err)
	}

	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(
		sentinel,
		[]byte("keep\n"),
		0o644,
	); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	err := WriteDirectory(
		context.Background(),
		root,
		[]File{
			{
				Path: "registry.json",
				Data: []byte("{}\n"),
			},
		},
	)
	if !errors.Is(err, ErrDestinationExists) {
		t.Errorf(
			"WriteDirectory() error = %v, want %v",
			err,
			ErrDestinationExists,
		)
	}

	got, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatalf("read sentinel: %v", readErr)
	}

	if string(got) != "keep\n" {
		t.Errorf(
			"sentinel = %q, want %q",
			got,
			"keep\n",
		)
	}

	assertNoWriterStaging(t, parent, "export")
}

func TestWriteDirectoryRejectsInvalidRoots(
	t *testing.T,
) {
	tests := []string{
		"",
		".",
		"/",
		"export/",
		"./export",
		"../export",
		"parent/../export",
		`parent\export`,
	}

	for _, root := range tests {
		t.Run(root, func(t *testing.T) {
			err := WriteDirectory(
				context.Background(),
				root,
				[]File{
					{
						Path: "registry.json",
						Data: []byte("{}\n"),
					},
				},
			)
			if !errors.Is(err, ErrInvalidOutputRoot) {
				t.Errorf(
					"WriteDirectory(%q) error = %v, want %v",
					root,
					err,
					ErrInvalidOutputRoot,
				)
			}
		})
	}
}

func TestWriteDirectoryRejectsInvalidLogicalPathsBeforeMutation(
	t *testing.T,
) {
	paths := []string{
		"",
		".",
		"../escape",
		"/absolute",
		"nodes/../escape",
		`nodes..\escape`,
		"./registry.json",
		"registry.json/..",
		"nodes//hash.json",
		"nodes/",
	}

	for _, logicalPath := range paths {
		t.Run(logicalPath, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "export")

			err := WriteDirectory(
				context.Background(),
				root,
				[]File{
					{
						Path: logicalPath,
						Data: []byte("unsafe\n"),
					},
				},
			)
			if !errors.Is(err, ErrInvalidOutputPath) {
				t.Errorf(
					"WriteDirectory() error = %v, want %v",
					err,
					ErrInvalidOutputPath,
				)
			}

			if _, statErr := os.Stat(root); !errors.Is(
				statErr,
				os.ErrNotExist,
			) {
				t.Errorf(
					"destination stat error = %v, want os.ErrNotExist",
					statErr,
				)
			}

			escape := filepath.Join(parent, "escape")
			if _, statErr := os.Stat(escape); !errors.Is(
				statErr,
				os.ErrNotExist,
			) {
				t.Errorf(
					"escape stat error = %v, want os.ErrNotExist",
					statErr,
				)
			}

			assertNoWriterStaging(
				t,
				parent,
				"export",
			)
		})
	}
}

func TestWriteDirectoryRejectsDuplicateLogicalPaths(
	t *testing.T,
) {
	parent := t.TempDir()
	root := filepath.Join(parent, "export")

	err := WriteDirectory(
		context.Background(),
		root,
		[]File{
			{
				Path: "registry.json",
				Data: []byte("first\n"),
			},
			{
				Path: "registry.json",
				Data: []byte("second\n"),
			},
		},
	)
	if !errors.Is(err, ErrDuplicateOutputPath) {
		t.Errorf(
			"WriteDirectory() error = %v, want %v",
			err,
			ErrDuplicateOutputPath,
		)
	}

	if _, statErr := os.Stat(root); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Errorf(
			"destination stat error = %v, want os.ErrNotExist",
			statErr,
		)
	}

	assertNoWriterStaging(t, parent, "export")
}

func TestWriteDirectoryRequiresExistingDirectoryParent(
	t *testing.T,
) {
	temporary := t.TempDir()
	missingParent := filepath.Join(temporary, "missing")
	parentFile := filepath.Join(temporary, "parent-file")

	if err := os.WriteFile(
		parentFile,
		[]byte("not a directory\n"),
		0o644,
	); err != nil {
		t.Fatalf("write parent file: %v", err)
	}

	tests := []struct {
		name string
		root string
	}{
		{
			name: "missing parent",
			root: filepath.Join(
				missingParent,
				"export",
			),
		},
		{
			name: "parent is file",
			root: filepath.Join(
				parentFile,
				"export",
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := WriteDirectory(
				context.Background(),
				test.root,
				[]File{
					{
						Path: "registry.json",
						Data: []byte("{}\n"),
					},
				},
			)
			if !errors.Is(err, ErrInvalidOutputRoot) {
				t.Errorf(
					"WriteDirectory() error = %v, want %v",
					err,
					ErrInvalidOutputRoot,
				)
			}
		})
	}

	if _, err := os.Stat(missingParent); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Errorf(
			"missing parent stat error = %v, want os.ErrNotExist",
			err,
		)
	}

	got, err := os.ReadFile(parentFile)
	if err != nil {
		t.Fatalf("read parent file: %v", err)
	}

	if string(got) != "not a directory\n" {
		t.Errorf(
			"parent file = %q, want unchanged",
			got,
		)
	}
}

func TestWriteDirectoryRejectsNilAndCanceledContexts(
	t *testing.T,
) {
	parent := t.TempDir()

	err := WriteDirectory(
		nil,
		filepath.Join(parent, "nil-context"),
		[]File{
			{
				Path: "registry.json",
				Data: []byte("{}\n"),
			},
		},
	)
	if !errors.Is(err, ErrInvalidContext) {
		t.Errorf(
			"nil context error = %v, want %v",
			err,
			ErrInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	root := filepath.Join(parent, "canceled")
	err = WriteDirectory(
		ctx,
		root,
		[]File{
			{
				Path: "registry.json",
				Data: []byte("{}\n"),
			},
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"canceled context error = %v, want context.Canceled",
			err,
		)
	}

	if _, statErr := os.Stat(root); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Errorf(
			"destination stat error = %v, want os.ErrNotExist",
			statErr,
		)
	}

	assertNoWriterStaging(t, parent, "canceled")
}

func TestWriteDirectoryCleansStagingAfterFilesystemFailures(
	t *testing.T,
) {
	tests := []string{
		"lstat",
		"mkdir-temp",
		"chmod",
		"mkdir-all",
		"write-file",
		"rename",
	}

	for _, failure := range tests {
		t.Run(failure, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "export")
			fileSystem := &faultFileSystem{
				failOperation: failure,
			}

			err := writeDirectory(
				context.Background(),
				root,
				[]File{
					{
						Path: "nodes/ab/hash.json",
						Data: []byte("{}\n"),
					},
				},
				fileSystem,
			)
			if !errors.Is(err, errInjectedFileSystem) {
				t.Errorf(
					"writeDirectory() error = %v, want %v",
					err,
					errInjectedFileSystem,
				)
			}

			if _, statErr := os.Stat(root); !errors.Is(
				statErr,
				os.ErrNotExist,
			) {
				t.Errorf(
					"destination stat error = %v, want os.ErrNotExist",
					statErr,
				)
			}

			assertNoWriterStaging(
				t,
				parent,
				"export",
			)
		})
	}
}

func TestWriteDirectoryChecksContextDuringSnapshot(
	t *testing.T,
) {
	tests := []struct {
		name        string
		files       []File
		cancelStat  int
		cancelWrite int
	}{
		{
			name: "after validation",
			files: []File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
			},
			cancelStat: 1,
		},
		{
			name: "between files",
			files: []File{
				{
					Path: "first.json",
					Data: []byte("first\n"),
				},
				{
					Path: "second.json",
					Data: []byte("second\n"),
				},
			},
			cancelWrite: 1,
		},
		{
			name: "after last file",
			files: []File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
			},
			cancelWrite: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "export")
			ctx, cancel := context.WithCancel(
				context.Background(),
			)
			fileSystem := &faultFileSystem{
				cancel:           cancel,
				cancelAfterLstat: test.cancelStat,
				cancelAfterWrite: test.cancelWrite,
			}

			err := writeDirectory(
				ctx,
				root,
				test.files,
				fileSystem,
			)
			if !errors.Is(err, context.Canceled) {
				t.Errorf(
					"writeDirectory() error = %v, want context.Canceled",
					err,
				)
			}

			if _, statErr := os.Stat(root); !errors.Is(
				statErr,
				os.ErrNotExist,
			) {
				t.Errorf(
					"destination stat error = %v, want os.ErrNotExist",
					statErr,
				)
			}

			assertNoWriterStaging(
				t,
				parent,
				"export",
			)
		})
	}
}

func TestWriteDirectoryDoesNotReplaceDestinationCreatedBeforePublish(
	t *testing.T,
) {
	parent := t.TempDir()
	root := filepath.Join(parent, "export")
	fileSystem := &faultFileSystem{
		createDestinationOnLstat: 2,
		destination:              root,
	}

	err := writeDirectory(
		context.Background(),
		root,
		[]File{
			{
				Path: "registry.json",
				Data: []byte("{}\n"),
			},
		},
		fileSystem,
	)
	if !errors.Is(err, ErrDestinationExists) {
		t.Errorf(
			"writeDirectory() error = %v, want %v",
			err,
			ErrDestinationExists,
		)
	}

	sentinel := filepath.Join(root, "sentinel")
	got, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatalf("read sentinel: %v", readErr)
	}

	if string(got) != "keep\n" {
		t.Errorf(
			"sentinel = %q, want %q",
			got,
			"keep\n",
		)
	}

	assertNoWriterStaging(t, parent, "export")
}

func assertNoWriterStaging(
	t *testing.T,
	parent string,
	base string,
) {
	t.Helper()

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent directory: %v", err)
	}

	prefix := "." + base + ".tmp-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			t.Errorf(
				"staging entry remains: %q",
				entry.Name(),
			)
		}
	}
}

type faultFileSystem struct {
	osFileSystem
	failOperation            string
	cancel                   context.CancelFunc
	cancelAfterLstat         int
	cancelAfterWrite         int
	createDestinationOnLstat int
	destination              string
	lstatCalls               int
	writeCalls               int
}

func (fileSystem *faultFileSystem) Lstat(
	name string,
) (os.FileInfo, error) {
	fileSystem.lstatCalls++

	if fileSystem.createDestinationOnLstat ==
		fileSystem.lstatCalls {
		if err := os.Mkdir(name, 0o755); err != nil {
			return nil, err
		}

		if err := os.WriteFile(
			filepath.Join(name, "sentinel"),
			[]byte("keep\n"),
			0o644,
		); err != nil {
			return nil, err
		}
	}

	if fileSystem.failOperation == "lstat" {
		return nil, errInjectedFileSystem
	}

	info, err := fileSystem.osFileSystem.Lstat(name)

	if fileSystem.cancelAfterLstat ==
		fileSystem.lstatCalls {
		fileSystem.cancel()
	}

	return info, err
}

func (fileSystem *faultFileSystem) MkdirTemp(
	directory string,
	pattern string,
) (string, error) {
	if fileSystem.failOperation == "mkdir-temp" {
		return "", errInjectedFileSystem
	}

	return fileSystem.osFileSystem.MkdirTemp(
		directory,
		pattern,
	)
}

func (fileSystem *faultFileSystem) Chmod(
	name string,
	mode os.FileMode,
) error {
	if fileSystem.failOperation == "chmod" {
		return errInjectedFileSystem
	}

	return fileSystem.osFileSystem.Chmod(name, mode)
}

func (fileSystem *faultFileSystem) MkdirAll(
	name string,
	mode os.FileMode,
) error {
	if fileSystem.failOperation == "mkdir-all" {
		return errInjectedFileSystem
	}

	return fileSystem.osFileSystem.MkdirAll(name, mode)
}

func (fileSystem *faultFileSystem) WriteFile(
	name string,
	data []byte,
	mode os.FileMode,
) error {
	if fileSystem.failOperation == "write-file" {
		return errInjectedFileSystem
	}

	err := fileSystem.osFileSystem.WriteFile(
		name,
		data,
		mode,
	)
	if err != nil {
		return err
	}

	fileSystem.writeCalls++
	if fileSystem.cancelAfterWrite ==
		fileSystem.writeCalls {
		fileSystem.cancel()
	}

	return nil
}

func (fileSystem *faultFileSystem) Rename(
	oldPath string,
	newPath string,
) error {
	if fileSystem.failOperation == "rename" {
		return errInjectedFileSystem
	}

	return fileSystem.osFileSystem.Rename(
		oldPath,
		newPath,
	)
}
