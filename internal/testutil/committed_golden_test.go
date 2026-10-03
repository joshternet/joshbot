package testutil

import (
	"path/filepath"
	"testing"
)

func TestCommittedGoldenFixture(t *testing.T) {
	goldenPath := filepath.Join("testdata", "golden", "mismatch.golden")
	actual := []byte("expected\n")

	if err := CheckGolden(goldenPath, actual); err != nil {
		t.Fatalf("CheckGolden() error = %v, want nil", err)
	}
}
