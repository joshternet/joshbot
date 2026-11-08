package discovery

import "testing"

func TestFleetByteBudgetPauseIntegration(t *testing.T) {
	t.Run("crawl", TestCrawlStopsForFleetByteBudget)
	t.Run("runner", TestCrawlRunnerFleetByteBudgetReleasesAndWaits)
}
