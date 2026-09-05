package publicdata

import (
	"context"
	"io/fs"
	"testing"
)

func TestReadDirectoryHonorsCancellationWhileWalking(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	directory := newTestSnapshotDirectory(
		map[string][]byte{
			"registry.json": []byte("{}\n"),
		},
	)
	directory.fileSystem =
		&cancelingReadDirFileSystem{
			FS:     directory.fileSystem,
			cancel: cancel,
		}

	got, err := readDirectory(
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
	assertReaderError(
		t,
		got,
		err,
		context.Canceled,
	)
}

type cancelingReadDirFileSystem struct {
	fs.FS

	cancel   context.CancelFunc
	canceled bool
}

func (fileSystem *cancelingReadDirFileSystem) ReadDir(
	name string,
) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(
		fileSystem.FS,
		name,
	)
	if err == nil &&
		name == "." &&
		!fileSystem.canceled {
		fileSystem.canceled = true
		fileSystem.cancel()
	}

	return entries, err
}
