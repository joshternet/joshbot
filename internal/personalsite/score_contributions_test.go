package personalsite

import "testing"

func TestContributionsFromReasonsClamping(t *testing.T) {
	t.Parallel()

	reasons := []ReasonCode{
		ReasonPersonSchema,
		ReasonHCard,
		ReasonRelMe,
		ReasonFeed,
		ReasonAboutLink,
		ReasonProjectsLink,
		ReasonBlogLink,
		ReasonNowLink,
		ReasonUsesLink,
		ReasonAuthorMetadata,
	}
	contributions, clamped := ContributionsFromReasons(reasons, 100)
	if len(contributions) != len(reasons) {
		t.Fatalf("contributions = %#v", contributions)
	}
	if !clamped {
		t.Fatal("expected clamping when positive weights exceed 100")
	}

	zero, clampedZero := ContributionsFromReasons(nil, 0)
	if len(zero) != 0 || clampedZero {
		t.Fatalf("empty = %#v clamped=%v", zero, clampedZero)
	}
}
