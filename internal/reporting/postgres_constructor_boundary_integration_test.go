package reporting

import "testing"

func TestPostgresConstructorBoundaryIntegrationRejectsNilPool(t *testing.T) {
	reader, err := newPostgresReader(
		nil,
		PostgresConfig{MaxPendingProbes: 1},
	)
	if err == nil {
		t.Fatal("newPostgresReader(nil) error = nil, want error")
	}
	if reader != nil {
		t.Errorf("newPostgresReader(nil) reader = %#v, want nil", reader)
	}
}
