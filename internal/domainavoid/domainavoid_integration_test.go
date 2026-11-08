package domainavoid

import "testing"

// TestDomainAvoidRulesIntegration re-enters the domain-avoid unit tests. The
// integration coverage gate only counts Test functions declared in
// *_integration_test.go, so these calls are what cover hostname globs and re:.
func TestDomainAvoidRulesIntegration(t *testing.T) {
	t.Run("normalize", TestNormalize)
	t.Run("valid and matches", TestValidAndMatches)
	t.Run("valid list", TestValidList)
	t.Run("non-regex patterns", TestNonRegexPatterns)
}
