package robots

import "testing"

func TestSchedulerEvictionAndPolitenessIntegration(t *testing.T) {
	t.Run("bounded and expired", TestRequestSchedulerBoundsInactiveOrigins)
	t.Run("pending spacing", TestSchedulerCapacityPreservesPendingSpacing)
	t.Run("active policy", TestSchedulerCapacityPreservesActivePolicy)
	t.Run("cached delay", TestCheckerRestoresCachedDelayAfterScheduleEviction)
	t.Run("active request", TestCheckerPinsScheduleDuringProtectedRequest)
	t.Run("robots redirect", TestCheckerPinsRedirectedRobotsSchedule)
	t.Run("scheduled getter validation", TestScheduledHopGetterStopsWhenWaitFails)
	t.Run("input validation", TestCheckerRejectsInvalidInputs)
}
