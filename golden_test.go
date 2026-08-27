package joshbot

import (
	"path/filepath"
	"testing"

	"github.com/joshternet/joshbot/internal/testutil"
)

func TestCommittedGoldenFixture(t *testing.T) {
	goldenPath := filepath.Join("testdata", "golden", "mismatch.golden")
	actual := []byte("expected\n")

	if err := testutil.CheckGolden(goldenPath, actual); err != nil {
		t.Fatalf("testutil.CheckGolden() error = %v, want nil", err)
	}
}
