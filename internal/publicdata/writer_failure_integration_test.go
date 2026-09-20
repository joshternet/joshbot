package publicdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errPublicDataWriterFailureIntegration = errors.New(
	"integration publicdata writer filesystem failure",
)

func TestPublicDataWriterFailureIntegrationValidatesInputs(
	t *testing.T,
) {
	files := []File{
		{
			Path: "registry.json",
			Data: []byte("{}\n"),
		},
	}

	if err := WriteDirectory(
		nil,
		filepath.Join(t.TempDir(), "nil-context"),
		files,
	); !errors.Is(err, ErrInvalidContext) {
		t.Errorf(
			"WriteDirectory(nil) error = %v, want %v",
			err,
			ErrInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := WriteDirectory(
		ctx,
		filepath.Join(t.TempDir(), "canceled"),
		files,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"WriteDirectory(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	invalidRoots := []string{
		"",
		".",
		string(os.PathSeparator),
		"export/",
		"./export",
		"../export",
		"parent/../export",
		`parent\export`,
	}

	for _, root := range invalidRoots {
		t.Run("root "+root, func(t *testing.T) {
			err := WriteDirectory(
				context.Background(),
				root,
				files,
			)
			if !errors.Is(
				err,
				ErrInvalidOutputRoot,
			) {
				t.Errorf(
					"WriteDirectory(%q) error = %v, want %v",
					root,
					err,
					ErrInvalidOutputRoot,
				)
			}
		})
	}

	invalidPaths := []string{
		"",
		".",
		"../escape",
		"/absolute",
		"nodes/../escape",
		`nodes\escape`,
		"./registry.json",
		"registry.json/..",
		"nodes//hash.json",
		"nodes/",
	}

	for _, logicalPath := range invalidPaths {
		t.Run("path "+logicalPath, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(
				parent,
				"export",
			)

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
			if !errors.Is(
				err,
				ErrInvalidOutputPath,
			) {
				t.Errorf(
					"WriteDirectory() error = %v, want %v",
					err,
					ErrInvalidOutputPath,
				)
			}

			publicDataWriterFailureIntegrationAssertNoStaging(
				t,
				parent,
				"export",
			)
		})
	}

	parent := t.TempDir()
	root := filepath.Join(parent, "duplicate")
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
			"duplicate WriteDirectory() error = %v, want %v",
			err,
			ErrDuplicateOutputPath,
		)
	}
}

func TestPublicDataWriterFailureIntegrationValidatesDestination(
	t *testing.T,
) {
	files := []File{
		{
			Path: "registry.json",
			Data: []byte("{}\n"),
		},
	}

	t.Run("missing parent", func(t *testing.T) {
		root := filepath.Join(
			t.TempDir(),
			"missing",
			"export",
		)

		err := WriteDirectory(
			context.Background(),
			root,
			files,
		)
		if !errors.Is(err, ErrInvalidOutputRoot) {
			t.Errorf(
				"WriteDirectory() error = %v, want %v",
				err,
				ErrInvalidOutputRoot,
			)
		}
	})

	t.Run("parent is file", func(t *testing.T) {
		parent := t.TempDir()
		parentFile := filepath.Join(
			parent,
			"parent-file",
		)
		if err := os.WriteFile(
			parentFile,
			[]byte("not a directory\n"),
			0o644,
		); err != nil {
			t.Fatalf(
				"write parent file: %v",
				err,
			)
		}

		err := WriteDirectory(
			context.Background(),
			filepath.Join(
				parentFile,
				"export",
			),
			files,
		)
		if !errors.Is(err, ErrInvalidOutputRoot) {
			t.Errorf(
				"WriteDirectory() error = %v, want %v",
				err,
				ErrInvalidOutputRoot,
			)
		}
	})

	t.Run("destination already exists", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "export")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatalf(
				"create destination: %v",
				err,
			)
		}

		err := WriteDirectory(
			context.Background(),
			root,
			files,
		)
		if !errors.Is(err, ErrDestinationExists) {
			t.Errorf(
				"WriteDirectory() error = %v, want %v",
				err,
				ErrDestinationExists,
			)
		}

		publicDataWriterFailureIntegrationAssertNoStaging(
			t,
			parent,
			"export",
		)
	})
}

func TestPublicDataWriterFailureIntegrationCleansUpFilesystemFailures(
	t *testing.T,
) {
	failures := []struct {
		operation string
		want      error
	}{
		{operation: "stat", want: ErrInvalidOutputRoot},
		{operation: "lstat", want: errPublicDataWriterFailureIntegration},
		{operation: "mkdir-temp", want: errPublicDataWriterFailureIntegration},
		{operation: "chmod", want: errPublicDataWriterFailureIntegration},
		{operation: "mkdir-all", want: errPublicDataWriterFailureIntegration},
		{operation: "write-file", want: errPublicDataWriterFailureIntegration},
		{operation: "rename", want: errPublicDataWriterFailureIntegration},
	}

	for _, failure := range failures {
		t.Run(failure.operation, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "export")
			fileSystem :=
				&publicDataWriterFailureIntegrationFileSystem{
					failOperation: failure.operation,
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
			if !errors.Is(
				err,
				failure.want,
			) {
				t.Errorf(
					"writeDirectory() error = %v, want %v",
					err,
					failure.want,
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

			publicDataWriterFailureIntegrationAssertNoStaging(
				t,
				parent,
				"export",
			)
		})
	}
}

func TestPublicDataWriterFailureIntegrationHonorsCancellation(
	t *testing.T,
) {
	tests := []struct {
		name        string
		files       []File
		cancelLstat int
		cancelWrite int
	}{
		{
			name: "after destination validation",
			files: []File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
			},
			cancelLstat: 1,
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

			fileSystem :=
				&publicDataWriterFailureIntegrationFileSystem{
					cancel:           cancel,
					cancelAfterLstat: test.cancelLstat,
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

			publicDataWriterFailureIntegrationAssertNoStaging(
				t,
				parent,
				"export",
			)
		})
	}
}

func TestPublicDataWriterFailureIntegrationDoesNotReplaceRacingDestination(
	t *testing.T,
) {
	parent := t.TempDir()
	root := filepath.Join(parent, "export")
	fileSystem :=
		&publicDataWriterFailureIntegrationFileSystem{
			createDestinationOnLstat: 2,
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
		t.Fatalf(
			"writeDirectory() error = %v, want %v",
			err,
			ErrDestinationExists,
		)
	}

	sentinel := filepath.Join(root, "sentinel")
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf(
			"read racing destination sentinel: %v",
			err,
		)
	}

	if string(data) != "keep\n" {
		t.Errorf(
			"sentinel = %q, want %q",
			data,
			"keep\n",
		)
	}

	publicDataWriterFailureIntegrationAssertNoStaging(
		t,
		parent,
		"export",
	)
}

type publicDataWriterFailureIntegrationFileSystem struct {
	osFileSystem

	failOperation string

	cancel           context.CancelFunc
	cancelAfterLstat int
	cancelAfterWrite int

	createDestinationOnLstat int
	lstatCalls               int
	writeCalls               int
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) Stat(
	name string,
) (os.FileInfo, error) {
	if fileSystem.failOperation == "stat" {
		return nil,
			errPublicDataWriterFailureIntegration
	}

	return fileSystem.osFileSystem.Stat(name)
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) Lstat(
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
		return nil,
			errPublicDataWriterFailureIntegration
	}

	info, err := fileSystem.osFileSystem.Lstat(name)

	if fileSystem.cancel != nil &&
		fileSystem.cancelAfterLstat ==
			fileSystem.lstatCalls {
		fileSystem.cancel()
	}

	return info, err
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) MkdirTemp(
	directory string,
	pattern string,
) (string, error) {
	if fileSystem.failOperation == "mkdir-temp" {
		return "",
			errPublicDataWriterFailureIntegration
	}

	return fileSystem.osFileSystem.MkdirTemp(
		directory,
		pattern,
	)
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) Chmod(
	name string,
	mode os.FileMode,
) error {
	if fileSystem.failOperation == "chmod" {
		return errPublicDataWriterFailureIntegration
	}

	return fileSystem.osFileSystem.Chmod(
		name,
		mode,
	)
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) MkdirAll(
	name string,
	mode os.FileMode,
) error {
	if fileSystem.failOperation == "mkdir-all" {
		return errPublicDataWriterFailureIntegration
	}

	return fileSystem.osFileSystem.MkdirAll(
		name,
		mode,
	)
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) WriteFile(
	name string,
	data []byte,
	mode os.FileMode,
) error {
	if fileSystem.failOperation == "write-file" {
		return errPublicDataWriterFailureIntegration
	}

	if err := fileSystem.osFileSystem.WriteFile(
		name,
		data,
		mode,
	); err != nil {
		return err
	}

	fileSystem.writeCalls++
	if fileSystem.cancel != nil &&
		fileSystem.cancelAfterWrite ==
			fileSystem.writeCalls {
		fileSystem.cancel()
	}

	return nil
}

func (fileSystem *publicDataWriterFailureIntegrationFileSystem) Rename(
	oldPath string,
	newPath string,
) error {
	if fileSystem.failOperation == "rename" {
		return errPublicDataWriterFailureIntegration
	}

	return fileSystem.osFileSystem.Rename(
		oldPath,
		newPath,
	)
}

func publicDataWriterFailureIntegrationAssertNoStaging(
	t *testing.T,
	parent string,
	base string,
) {
	t.Helper()

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf(
			"read parent directory: %v",
			err,
		)
	}

	prefix := "." + base + ".tmp-"
	for _, entry := range entries {
		if strings.HasPrefix(
			entry.Name(),
			prefix,
		) {
			t.Errorf(
				"staging entry remains: %q",
				entry.Name(),
			)
		}
	}
}
