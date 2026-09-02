package publicdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrInvalidContext means WriteDirectory received a nil context.
	ErrInvalidContext = errors.New(
		"publicdata: context is nil",
	)

	// ErrInvalidOutputRoot means the requested destination is unsafe or its
	// parent is unavailable.
	ErrInvalidOutputRoot = errors.New(
		"publicdata: output root is invalid",
	)

	// ErrDestinationExists means the requested output root already exists.
	ErrDestinationExists = errors.New(
		"publicdata: destination exists",
	)

	// ErrInvalidOutputPath means a logical snapshot path is unsafe.
	ErrInvalidOutputPath = errors.New(
		"publicdata: output path is invalid",
	)

	// ErrDuplicateOutputPath means a snapshot repeats a logical path.
	ErrDuplicateOutputPath = errors.New(
		"publicdata: duplicate output path",
	)
)

type fileSystem interface {
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	MkdirTemp(string, string) (string, error)
	Chmod(string, os.FileMode) error
	MkdirAll(string, os.FileMode) error
	WriteFile(string, []byte, os.FileMode) error
	Rename(string, string) error
	RemoveAll(string) error
}

type osFileSystem struct{}

func (osFileSystem) Stat(
	name string,
) (os.FileInfo, error) {
	return os.Stat(name)
}

func (osFileSystem) Lstat(
	name string,
) (os.FileInfo, error) {
	return os.Lstat(name)
}

func (osFileSystem) MkdirTemp(
	directory string,
	pattern string,
) (string, error) {
	return os.MkdirTemp(directory, pattern)
}

func (osFileSystem) Chmod(
	name string,
	mode os.FileMode,
) error {
	return os.Chmod(name, mode)
}

func (osFileSystem) MkdirAll(
	name string,
	mode os.FileMode,
) error {
	return os.MkdirAll(name, mode)
}

func (osFileSystem) WriteFile(
	name string,
	data []byte,
	mode os.FileMode,
) error {
	return os.WriteFile(name, data, mode)
}

func (osFileSystem) Rename(
	oldPath string,
	newPath string,
) error {
	return os.Rename(oldPath, newPath)
}

func (osFileSystem) RemoveAll(name string) error {
	return os.RemoveAll(name)
}

// WriteDirectory materializes a complete snapshot at a previously absent root.
//
// The complete tree is built in a same-parent staging directory and renamed
// into place only after every file has been written successfully.
func WriteDirectory(
	ctx context.Context,
	root string,
	files []File,
) error {
	return writeDirectory(
		ctx,
		root,
		files,
		osFileSystem{},
	)
}

func writeDirectory(
	ctx context.Context,
	root string,
	files []File,
	fileSystem fileSystem,
) error {
	if err := checkWriterContext(ctx); err != nil {
		return err
	}

	cleanRoot, parent, base, err :=
		outputRootParts(root)
	if err != nil {
		return err
	}

	if err := validateLogicalFiles(files); err != nil {
		return err
	}

	if err := ensureDestinationAvailable(
		fileSystem,
		parent,
		cleanRoot,
	); err != nil {
		return err
	}

	if err := checkWriterContext(ctx); err != nil {
		return err
	}

	staging, err := fileSystem.MkdirTemp(
		parent,
		"."+base+".tmp-",
	)
	if err != nil {
		return fmt.Errorf(
			"publicdata: create staging directory: %w",
			err,
		)
	}
	defer func() {
		_ = fileSystem.RemoveAll(staging)
	}()

	if err := fileSystem.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf(
			"publicdata: set staging permissions: %w",
			err,
		)
	}

	for _, file := range files {
		if err := checkWriterContext(ctx); err != nil {
			return err
		}

		destination := filepath.Join(
			staging,
			filepath.FromSlash(file.Path),
		)
		if err := fileSystem.MkdirAll(
			filepath.Dir(destination),
			0o755,
		); err != nil {
			return fmt.Errorf(
				"publicdata: create snapshot directory: %w",
				err,
			)
		}

		if err := fileSystem.WriteFile(
			destination,
			file.Data,
			0o644,
		); err != nil {
			return fmt.Errorf(
				"publicdata: write snapshot file: %w",
				err,
			)
		}
	}

	if err := checkWriterContext(ctx); err != nil {
		return err
	}

	if err := ensureDestinationAvailable(
		fileSystem,
		parent,
		cleanRoot,
	); err != nil {
		return err
	}

	if err := fileSystem.Rename(
		staging,
		cleanRoot,
	); err != nil {
		return fmt.Errorf(
			"publicdata: publish snapshot directory: %w",
			err,
		)
	}

	return nil
}

func checkWriterContext(
	ctx context.Context,
) error {
	if ctx == nil {
		return ErrInvalidContext
	}

	return ctx.Err()
}

func outputRootParts(
	root string,
) (string, string, string, error) {
	cleanRoot := filepath.Clean(root)
	if root == "" ||
		cleanRoot != root ||
		cleanRoot == "." ||
		cleanRoot == string(os.PathSeparator) ||
		strings.Contains(root, "\\") ||
		hasRootDotSegment(root) {
		return "", "", "", ErrInvalidOutputRoot
	}

	return cleanRoot,
		filepath.Dir(cleanRoot),
		filepath.Base(cleanRoot),
		nil
}

func hasRootDotSegment(root string) bool {
	for _, segment := range strings.Split(
		root,
		string(os.PathSeparator),
	) {
		if segment == "." || segment == ".." {
			return true
		}
	}

	return false
}

func validateLogicalFiles(files []File) error {
	seen := make(map[string]struct{}, len(files))

	for _, file := range files {
		if !validLogicalPath(file.Path) {
			return fmt.Errorf(
				"%w: %q",
				ErrInvalidOutputPath,
				file.Path,
			)
		}

		if _, exists := seen[file.Path]; exists {
			return fmt.Errorf(
				"%w: %q",
				ErrDuplicateOutputPath,
				file.Path,
			)
		}

		seen[file.Path] = struct{}{}
	}

	return nil
}

func validLogicalPath(logicalPath string) bool {
	if logicalPath == "" ||
		logicalPath == "." ||
		path.IsAbs(logicalPath) ||
		strings.HasPrefix(logicalPath, "/") ||
		strings.Contains(logicalPath, "\\") ||
		path.Clean(logicalPath) != logicalPath {
		return false
	}

	for _, segment := range strings.Split(
		logicalPath,
		"/",
	) {
		if segment == "" ||
			segment == "." ||
			segment == ".." {
			return false
		}
	}

	return true
}

func ensureDestinationAvailable(
	fileSystem fileSystem,
	parent string,
	root string,
) error {
	parentInfo, err := fileSystem.Stat(parent)
	if err != nil {
		return fmt.Errorf(
			"%w: parent: %v",
			ErrInvalidOutputRoot,
			err,
		)
	}

	if !parentInfo.IsDir() {
		return fmt.Errorf(
			"%w: parent is not a directory",
			ErrInvalidOutputRoot,
		)
	}

	_, err = fileSystem.Lstat(root)
	if err == nil {
		return ErrDestinationExists
	}

	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(
			"publicdata: inspect destination: %w",
			err,
		)
	}

	return nil
}
