package robots

import "testing"

// TestSchedulerEvictionAndPolitenessIntegration re-enters the scheduler unit
// tests. The integration coverage gate runs only Test functions declared in
// *_integration_test.go, so these calls are what count neighborhood pacing.
func TestSchedulerEvictionAndPolitenessIntegration(t *testing.T) {
	t.Run("bounded and expired", TestRequestSchedulerBoundsInactiveOrigins)
	t.Run("pending spacing", TestSchedulerCapacityPreservesPendingSpacing)
	t.Run("active policy", TestSchedulerCapacityPreservesActivePolicy)
	t.Run("cached delay", TestCheckerRestoresCachedDelayAfterScheduleEviction)
	t.Run("active request", TestCheckerPinsScheduleDuringProtectedRequest)
	t.Run("robots redirect", TestCheckerPinsRedirectedRobotsSchedule)
	t.Run("scheduled getter validation", TestScheduledHopGetterStopsWhenWaitFails)
	t.Run("input validation", TestCheckerRejectsInvalidInputs)
	t.Run("neighborhood key", TestNeighborhoodKey)
	t.Run("ip hosts", TestIPHostsDoNotShareANeighborhood)
	t.Run("shared minimum", TestSiblingHostsShareOperatorMinimum)
	t.Run("origin crawl delay", TestSiblingDoesNotInheritOriginCrawlDelay)
	t.Run("neighborhood capacity", TestAcquireTargetReleasesOriginWhenNeighborhoodIsFull)
}
