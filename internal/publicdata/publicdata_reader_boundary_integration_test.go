package publicdata

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

type publicDataReaderBoundaryIntegrationDirectory struct {
	fileSystem fs.FS
}

func (directory *publicDataReaderBoundaryIntegrationDirectory) FS() fs.FS {
	return directory.fileSystem
}

func (directory *publicDataReaderBoundaryIntegrationDirectory) Lstat(
	name string,
) (fs.FileInfo, error) {
	return fs.Stat(directory.fileSystem, name)
}

func (directory *publicDataReaderBoundaryIntegrationDirectory) Open(
	name string,
) (snapshotFile, error) {
	return directory.fileSystem.Open(name)
}

func (*publicDataReaderBoundaryIntegrationDirectory) Close() error {
	return nil
}

func TestPublicDataReaderBoundaryIntegrationCancellationAfterOpen(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())

	directory := &publicDataReaderBoundaryIntegrationDirectory{
		fileSystem: fstest.MapFS{
			"registry.json": &fstest.MapFile{
				Data: []byte("{}\n"),
				Mode: 0o644,
			},
		},
	}

	files, err := readDirectory(
		ctx,
		"snapshot",
		readerLimits{
			maxFiles:      1,
			maxFileBytes:  16,
			maxTotalBytes: 16,
		},
		func(string) (snapshotDirectory, error) {
			cancel()
			return directory, nil
		},
	)

	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"readDirectory() error = %v, want context.Canceled",
			err,
		)
	}

	if files != nil {
		t.Errorf(
			"readDirectory() files = %#v, want nil",
			files,
		)
	}
}

func TestPublicDataReaderBoundaryIntegrationRejectsMalformedPath(
	t *testing.T,
) {
	directory := &publicDataReaderBoundaryIntegrationDirectory{
		fileSystem: fstest.MapFS{
			"README.md": &fstest.MapFile{
				Data: []byte("unexpected\n"),
				Mode: 0o644,
			},
			"registry.json": &fstest.MapFile{
				Data: []byte("{}\n"),
				Mode: 0o644,
			},
		},
	}

	files, err := readDirectory(
		context.Background(),
		"snapshot",
		readerLimits{
			maxFiles:      2,
			maxFileBytes:  32,
			maxTotalBytes: 64,
		},
		func(string) (snapshotDirectory, error) {
			return directory, nil
		},
	)

	if !errors.Is(err, ErrInvalidSnapshotEntry) {
		t.Errorf(
			"readDirectory() error = %v, want %v",
			err,
			ErrInvalidSnapshotEntry,
		)
	}

	if files != nil {
		t.Errorf(
			"readDirectory() files = %#v, want nil",
			files,
		)
	}
}

func TestPublicDataReaderBoundaryIntegrationPathValidation(
	t *testing.T,
) {
	if validSnapshotPath("README.md") {
		t.Error(
			"validSnapshotPath(README.md) = true, want false",
		)
	}

	if isLowerHex("0g") {
		t.Error(
			"isLowerHex(0g) = true, want false",
		)
	}
}
