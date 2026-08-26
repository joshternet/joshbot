package joshbot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckGoldenUpdatesOnlyWhenRequested(t *testing.T) {
	expected := []byte("expected\n")
	actual := []byte("actual\n")

	tests := []struct {
		name    string
		update  bool
		want    []byte
		wantErr bool
	}{
		{
			name:    "normal comparison preserves fixture",
			update:  false,
			want:    expected,
			wantErr: true,
		},
		{
			name:    "explicit update replaces fixture",
			update:  true,
			want:    actual,
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			goldenPath := filepath.Join(t.TempDir(), "example.golden")

			if err := os.WriteFile(goldenPath, expected, 0o600); err != nil {
				t.Fatalf("create golden fixture: %v", err)
			}

			err := checkGolden(goldenPath, actual, test.update)
			if (err != nil) != test.wantErr {
				t.Fatalf("checkGolden() error = %v, wantErr %t", err, test.wantErr)
			}

			got, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden fixture: %v", err)
			}

			if !bytes.Equal(got, test.want) {
				t.Fatalf("golden fixture = %q, want %q", got, test.want)
			}
		})
	}
}
