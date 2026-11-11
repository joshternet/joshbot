package discovery

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
	"github.com/joshternet/joshbot/internal/robots"
)

func TestLivePersonalSiteQualifyKnownSites(t *testing.T) {
	if os.Getenv("JOSHBOT_LIVE_PERSONAL_SITE") != "1" {
		t.Skip("set JOSHBOT_LIVE_PERSONAL_SITE=1 to run live qualify checks")
	}

	checker, err := robots.NewCheckerWithHTTPConfig(
		net.DefaultResolver,
		&net.Dialer{Timeout: 15 * time.Second},
		0,
		nil,
		robots.DefaultHTTPConfig(),
		nil,
	)
	if err != nil {
		t.Fatalf("NewCheckerWithHTTPConfig() error = %v", err)
	}

	cases := []struct {
		rawOrigin string
		minScore  int
		maxScore  int
	}{
		{
			rawOrigin: "https://joshuamorris.info",
			minScore:  1,
			maxScore:  100,
		},
		{
			rawOrigin: "https://oldnavy.gap.com",
			minScore:  0,
			maxScore:  40,
		},
	}

	for _, test := range cases {
		t.Run(test.rawOrigin, func(t *testing.T) {
			candidate, parseErr := origin.Parse(test.rawOrigin)
			if parseErr != nil {
				t.Fatalf("Parse(%q) error = %v", test.rawOrigin, parseErr)
			}
			ctx, cancel := context.WithTimeout(
				context.Background(),
				45*time.Second,
			)
			defer cancel()

			qualification, qualifyErr := QualifyPersonalSite(
				ctx,
				checker,
				candidate,
			)
			if qualifyErr != nil {
				t.Fatalf("QualifyPersonalSite() error = %v", qualifyErr)
			}
			if qualification.Outcome != personalsite.QualificationAssessed {
				t.Fatalf(
					"outcome = %s status=%d, want assessed",
					qualification.Outcome,
					qualification.HTTPStatus,
				)
			}
			score := qualification.Assessment.Score
			if score < test.minScore || score > test.maxScore {
				t.Fatalf(
					"score = %d reasons=%v, want [%d,%d]",
					score,
					qualification.Assessment.Reasons,
					test.minScore,
					test.maxScore,
				)
			}
			t.Logf(
				"score=%d truncated=%v reasons=%v",
				score,
				qualification.Truncated,
				qualification.Assessment.Reasons,
			)
		})
	}
}
