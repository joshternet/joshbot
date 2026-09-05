package publicdata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
)

const (
	// maxSnapshotFiles permits registry.json and up to 4,096 node files.
	maxSnapshotFiles = 4_097

	// maxSnapshotFileBytes permits a growing registry document while
	// preventing a single file from consuming unbounded memory.
	maxSnapshotFileBytes int64 = 8 * 1024 * 1024

	// maxSnapshotTotalBytes bounds the complete in-memory snapshot.
	maxSnapshotTotalBytes int64 = 32 * 1024 * 1024
)

var (
	// ErrInvalidSnapshotRoot means the snapshot root is empty, unavailable,
	// not a directory, or a symbolic link.
	ErrInvalidSnapshotRoot = errors.New(
		"publicdata: snapshot root is invalid",
	)

	// ErrInvalidSnapshotEntry means the snapshot contains a symbolic link,
	// non-regular file, unexpected directory, or invalid logical file path.
	ErrInvalidSnapshotEntry = errors.New(
		"publicdata: snapshot entry is invalid",
	)

	// ErrMissingRegistry means the snapshot does not contain registry.json.
	ErrMissingRegistry = errors.New(
		"publicdata: registry.json is missing",
	)

	// ErrSnapshotLimitExceeded means the snapshot exceeds its fixed file
	// count, individual file size, or total byte limit.
	ErrSnapshotLimitExceeded = errors.New(
		"publicdata: snapshot resource limit exceeded",
	)
)

var defaultReaderLimits = readerLimits{
	maxFiles:      maxSnapshotFiles,
	maxFileBytes:  maxSnapshotFileBytes,
	maxTotalBytes: maxSnapshotTotalBytes,
}

type readerLimits struct {
	maxFiles      int
	maxFileBytes  int64
	maxTotalBytes int64
}

type snapshotFile interface {
	Read([]byte) (int, error)
	Stat() (fs.FileInfo, error)
	Close() error
}

type snapshotDirectory interface {
	FS() fs.FS
	Lstat(string) (fs.FileInfo, error)
	Open(string) (snapshotFile, error)
	Close() error
}

type snapshotDirectoryOpener func(
	string,
) (snapshotDirectory, error)

type osSnapshotDirectory struct {
	root *os.Root
}

func (directory *osSnapshotDirectory) FS() fs.FS {
	return directory.root.FS()
}

func (directory *osSnapshotDirectory) Lstat(
	name string,
) (fs.FileInfo, error) {
	return directory.root.Lstat(name)
}

func (directory *osSnapshotDirectory) Open(
	name string,
) (snapshotFile, error) {
	file, err := directory.root.Open(name)

	return file, err
}

func (directory *osSnapshotDirectory) Close() error {
	return directory.root.Close()
}

// ReadDirectory reads a complete deterministic public registry snapshot.
//
// The reader preserves exact file bytes, returns files in lexical path order,
// rejects symbolic links and non-regular files, and applies fixed resource
// limits before returning the in-memory snapshot.
func ReadDirectory(
	ctx context.Context,
	root string,
) ([]File, error) {
	return readDirectory(
		ctx,
		root,
		defaultReaderLimits,
		openSnapshotDirectory,
	)
}

func readDirectory(
	ctx context.Context,
	root string,
	limits readerLimits,
	openDirectory snapshotDirectoryOpener,
) ([]File, error) {
	if err := checkReaderContext(ctx); err != nil {
		return nil, err
	}

	if root == "" {
		return nil, ErrInvalidSnapshotRoot
	}

	directory, err := openDirectory(root)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %w",
			ErrInvalidSnapshotRoot,
			err,
		)
	}
	defer func() {
		_ = directory.Close()
	}()

	files := make([]File, 0)
	var totalBytes int64
	hasRegistry := false

	walkErr := fs.WalkDir(
		directory.FS(),
		".",
		func(
			logicalPath string,
			entry fs.DirEntry,
			entryErr error,
		) error {
			if err := checkReaderContext(ctx); err != nil {
				return err
			}

			if entryErr != nil {
				return fmt.Errorf(
					"publicdata: walk snapshot: %w",
					entryErr,
				)
			}

			if logicalPath == "." {
				return nil
			}

			if entry.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf(
					"%w: %q is a symbolic link",
					ErrInvalidSnapshotEntry,
					logicalPath,
				)
			}

			if entry.IsDir() {
				if !validSnapshotDirectory(
					logicalPath,
				) {
					return fmt.Errorf(
						"%w: unexpected directory %q",
						ErrInvalidSnapshotEntry,
						logicalPath,
					)
				}

				return nil
			}

			if !validSnapshotPath(logicalPath) {
				return fmt.Errorf(
					"%w: unexpected path %q",
					ErrInvalidSnapshotEntry,
					logicalPath,
				)
			}

			info, err := directory.Lstat(
				logicalPath,
			)
			if err != nil {
				return fmt.Errorf(
					"publicdata: inspect snapshot entry %q: %w",
					logicalPath,
					err,
				)
			}

			if info.Mode()&fs.ModeSymlink != 0 ||
				!info.Mode().IsRegular() {
				return fmt.Errorf(
					"%w: %q is not a regular file",
					ErrInvalidSnapshotEntry,
					logicalPath,
				)
			}

			if len(files) >= limits.maxFiles {
				return fmt.Errorf(
					"%w: file count exceeds %d",
					ErrSnapshotLimitExceeded,
					limits.maxFiles,
				)
			}

			remainingBytes :=
				limits.maxTotalBytes - totalBytes
			readLimit := limits.maxFileBytes
			if remainingBytes < readLimit {
				readLimit = remainingBytes
			}

			data, err := readSnapshotFile(
				ctx,
				directory,
				logicalPath,
				readLimit,
			)
			if err != nil {
				return err
			}

			totalBytes += int64(len(data))
			files = append(
				files,
				File{
					Path: logicalPath,
					Data: data,
				},
			)

			if logicalPath == "registry.json" {
				hasRegistry = true
			}

			return nil
		},
	)
	if walkErr != nil {
		return nil, walkErr
	}

	if !hasRegistry {
		return nil, ErrMissingRegistry
	}

	sort.Slice(files, func(left, right int) bool {
		return files[left].Path < files[right].Path
	})

	return files, nil
}

func openSnapshotDirectory(
	root string,
) (snapshotDirectory, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %v",
			ErrInvalidSnapshotRoot,
			err,
		)
	}

	if info.Mode()&fs.ModeSymlink != 0 ||
		!info.IsDir() {
		return nil, ErrInvalidSnapshotRoot
	}

	opened, err := os.OpenRoot(root)

	return &osSnapshotDirectory{
		root: opened,
	}, err
}

func readSnapshotFile(
	ctx context.Context,
	directory snapshotDirectory,
	logicalPath string,
	maxBytes int64,
) ([]byte, error) {
	file, err := directory.Open(logicalPath)
	if err != nil {
		return nil, fmt.Errorf(
			"publicdata: open snapshot entry %q: %w",
			logicalPath,
			err,
		)
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf(
			"publicdata: inspect opened snapshot entry %q: %w",
			logicalPath,
			err,
		)
	}

	if info.Mode()&fs.ModeSymlink != 0 ||
		!info.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"%w: %q changed before it was read",
			ErrInvalidSnapshotEntry,
			logicalPath,
		)
	}

	data, err := io.ReadAll(
		io.LimitReader(
			contextSnapshotReader{
				ctx:    ctx,
				reader: file,
			},
			maxBytes+1,
		),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"publicdata: read snapshot entry %q: %w",
			logicalPath,
			err,
		)
	}

	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf(
			"%w: %q exceeds %d bytes",
			ErrSnapshotLimitExceeded,
			logicalPath,
			maxBytes,
		)
	}

	return data, nil
}

type contextSnapshotReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextSnapshotReader) Read(
	buffer []byte,
) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}

	return reader.reader.Read(buffer)
}

func checkReaderContext(
	ctx context.Context,
) error {
	if ctx == nil {
		return ErrInvalidContext
	}

	return ctx.Err()
}

func validSnapshotDirectory(
	logicalPath string,
) bool {
	if logicalPath == "nodes" {
		return true
	}

	parts := strings.Split(logicalPath, "/")

	return len(parts) == 2 &&
		parts[0] == "nodes" &&
		len(parts[1]) == 2 &&
		isLowerHex(parts[1])
}

func validSnapshotPath(logicalPath string) bool {
	if logicalPath == "registry.json" {
		return true
	}

	parts := strings.Split(logicalPath, "/")
	if len(parts) != 3 ||
		parts[0] != "nodes" ||
		len(parts[1]) != 2 ||
		!isLowerHex(parts[1]) ||
		!strings.HasSuffix(parts[2], ".json") {
		return false
	}

	hash := strings.TrimSuffix(
		parts[2],
		".json",
	)

	return len(hash) == 64 &&
		isLowerHex(hash) &&
		strings.HasPrefix(hash, parts[1])
}

func isLowerHex(value string) bool {
	for index := 0; index < len(value); index++ {
		if !isLowerHexByte(value[index]) {
			return false
		}
	}

	return true
}

func isLowerHexByte(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f'
}
