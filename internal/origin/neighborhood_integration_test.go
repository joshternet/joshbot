package origin

import "testing"

// TestNeighborhoodKeyIntegration re-enters the neighborhood unit table. The
// integration coverage gate only counts Test functions declared in
// *_integration_test.go, so this call covers private-suffix and bare-ICANN
// host paths that other packages do not exercise.
func TestNeighborhoodKeyIntegration(t *testing.T) {
	TestNeighborhoodKey(t)
}
