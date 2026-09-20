// This bridge re-executes the package's existing scenario tests during
// the independent integration-coverage pass without changing production code.
package discovery

import "testing"

func TestIntegrationCoverageBridge(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "TestCrawlResultContainsOnlyEphemeralStatistics", run: TestCrawlResultContainsOnlyEphemeralStatistics},
		{name: "TestCrawlRunnerCompletesDurableRetryState", run: TestCrawlRunnerCompletesDurableRetryState},
		{name: "TestCrawlRunnerConfigurationValidation", run: TestCrawlRunnerConfigurationValidation},
		{name: "TestCrawlRunnerContinuesAfterBackedOffFailure", run: TestCrawlRunnerContinuesAfterBackedOffFailure},
		{name: "TestCrawlRunnerDoesNotPersistTransientFailureAfterPartialSuccess", run: TestCrawlRunnerDoesNotPersistTransientFailureAfterPartialSuccess},
		{name: "TestCrawlRunnerDoesNothingWithoutSource", run: TestCrawlRunnerDoesNothingWithoutSource},
		{name: "TestCrawlRunnerObservesClaimedLifecycle", run: TestCrawlRunnerObservesClaimedLifecycle},
		{name: "TestCrawlRunnerObservesPausedAndControlFailure", run: TestCrawlRunnerObservesPausedAndControlFailure},
		{name: "TestCrawlRunnerPersistsTypedCrawlResultFailure", run: TestCrawlRunnerPersistsTypedCrawlResultFailure},
		{name: "TestCrawlRunnerProcessesAvailableSourcesBeforeWaiting", run: TestCrawlRunnerProcessesAvailableSourcesBeforeWaiting},
		{name: "TestCrawlRunnerProcessesOneSource", run: TestCrawlRunnerProcessesOneSource},
		{name: "TestCrawlRunnerReturnsClaimFailure", run: TestCrawlRunnerReturnsClaimFailure},
		{name: "TestCrawlRunnerReturnsFatalCrawlFailure", run: TestCrawlRunnerReturnsFatalCrawlFailure},
		{name: "TestCrawlRunnerReturnsParentCancellation", run: TestCrawlRunnerReturnsParentCancellation},
		{name: "TestCrawlRunnerRunReturnsParentCancellationFromFailure", run: TestCrawlRunnerRunReturnsParentCancellationFromFailure},
		{name: "TestCrawlRunnerRunReturnsRunOnceFailure", run: TestCrawlRunnerRunReturnsRunOnceFailure},
		{name: "TestCrawlRunnerValidatesStoredState", run: TestCrawlRunnerValidatesStoredState},
		{name: "TestCrawlRunnerDoesNothingWithoutSource", run: TestCrawlRunnerDoesNothingWithoutSource},
		{name: "TestDiscoveryHelpers", run: TestDiscoveryHelpers},
		{name: "TestExtractPageLinksCanonicalizesIDNA", run: TestExtractPageLinksCanonicalizesIDNA},
		{name: "TestExtractPageLinksCanonicalizesIPv6", run: TestExtractPageLinksCanonicalizesIPv6},
		{name: "TestExtractPageLinksClassifiesNavigableHyperlinks", run: TestExtractPageLinksClassifiesNavigableHyperlinks},
		{name: "TestExtractPageLinksHonorsDocumentBase", run: TestExtractPageLinksHonorsDocumentBase},
		{name: "TestExtractPageLinksOrderIsDeterministic", run: TestExtractPageLinksOrderIsDeterministic},
		{name: "TestExtractPageLinksPreservesContentBoundaries", run: TestExtractPageLinksPreservesContentBoundaries},
		{name: "TestExtractPageLinksPreservesQueriesAndCollapsesFragments", run: TestExtractPageLinksPreservesQueriesAndCollapsesFragments},
		{name: "TestExtractPageLinksRejectsInvalidInput", run: TestExtractPageLinksRejectsInvalidInput},
		{name: "TestExtractPageLinksReturnsEveryLargeDirectoryCandidate", run: TestExtractPageLinksReturnsEveryLargeDirectoryCandidate},
		{name: "TestFetchPageSeparatesRetryableAndPermanentCategories", run: TestFetchPageSeparatesRetryableAndPermanentCategories},
		{name: "TestFetchPageTelemetryClassifiesResponseFailures", run: TestFetchPageTelemetryClassifiesResponseFailures},
		{name: "TestHTMLDecodingBoundsAndErrors", run: TestHTMLDecodingBoundsAndErrors},
		{name: "TestMultiPageCrawlerAbortsOnCandidateStoreFailure", run: TestMultiPageCrawlerAbortsOnCandidateStoreFailure},
		{name: "TestMultiPageCrawlerBoundsRedirectsAndContinues", run: TestMultiPageCrawlerBoundsRedirectsAndContinues},
		{name: "TestMultiPageCrawlerCarriesTransientRootFailure", run: TestMultiPageCrawlerCarriesTransientRootFailure},
		{name: "TestMultiPageCrawlerCollapsesCyclesAndDuplicates", run: TestMultiPageCrawlerCollapsesCyclesAndDuplicates},
		{name: "TestMultiPageCrawlerContinuesAfterPageTimeout", run: TestMultiPageCrawlerContinuesAfterPageTimeout},
		{name: "TestMultiPageCrawlerDeduplicatesCandidatesAcrossPages", run: TestMultiPageCrawlerDeduplicatesCandidatesAcrossPages},
		{name: "TestMultiPageCrawlerDoesNotRetryPartiallySuccessfulCrawl", run: TestMultiPageCrawlerDoesNotRetryPartiallySuccessfulCrawl},
		{name: "TestMultiPageCrawlerFinalizesCandidateAdmissionAfterAllPages", run: TestMultiPageCrawlerFinalizesCandidateAdmissionAfterAllPages},
		{name: "TestMultiPageCrawlerFollowsAndDeduplicatesSameOriginRedirect", run: TestMultiPageCrawlerFollowsAndDeduplicatesSameOriginRedirect},
		{name: "TestMultiPageCrawlerHonorsDepth", run: TestMultiPageCrawlerHonorsDepth},
		{name: "TestMultiPageCrawlerObeysPageBudgetExactly", run: TestMultiPageCrawlerObeysPageBudgetExactly},
		{name: "TestMultiPageCrawlerPacesFrontierRequests", run: TestMultiPageCrawlerPacesFrontierRequests},
		{name: "TestMultiPageCrawlerPreservesParentCancellation", run: TestMultiPageCrawlerPreservesParentCancellation},
		{name: "TestMultiPageCrawlerPropagatesTelemetryFailures", run: TestMultiPageCrawlerPropagatesTelemetryFailures},
		{name: "TestMultiPageCrawlerRecordsCrossOriginRedirectAndContinues", run: TestMultiPageCrawlerRecordsCrossOriginRedirectAndContinues},
		{name: "TestMultiPageCrawlerRecordsSafePageTelemetry", run: TestMultiPageCrawlerRecordsSafePageTelemetry},
		{name: "TestMultiPageCrawlerReturnsCandidateFinalizationFailure", run: TestMultiPageCrawlerReturnsCandidateFinalizationFailure},
		{name: "TestMultiPageCrawlerRootFailureEndsAttempt", run: TestMultiPageCrawlerRootFailureEndsAttempt},
		{name: "TestMultiPageCrawlerSkipsRemotePageFailuresAndContinues", run: TestMultiPageCrawlerSkipsRemotePageFailuresAndContinues},
		{name: "TestMultiPageCrawlerSkipsUnsupportedAndOversizedPages", run: TestMultiPageCrawlerSkipsUnsupportedAndOversizedPages},
		{name: "TestMultiPageCrawlerStreamsEveryLargeDirectoryCandidate", run: TestMultiPageCrawlerStreamsEveryLargeDirectoryCandidate},
		{name: "TestMultiPageCrawlerTelemetryValidationAndSanitizing", run: TestMultiPageCrawlerTelemetryValidationAndSanitizing},
		{name: "TestMultiPageCrawlerUsesBreadthFirstDocumentOrder", run: TestMultiPageCrawlerUsesBreadthFirstDocumentOrder},
		{name: "TestMultiPageCrawlerUsesSummaryTelemetry", run: TestMultiPageCrawlerUsesSummaryTelemetry},
		{name: "TestMultiPageCrawlerValidatesConfigurationAndDependencies", run: TestMultiPageCrawlerValidatesConfigurationAndDependencies},
		{name: "TestNewCrawlRunnerValidatesDependenciesAndConfiguration", run: TestNewCrawlRunnerValidatesDependenciesAndConfiguration},
		{name: "TestPageLinksContainsNoPageContentStorage", run: TestPageLinksContainsNoPageContentStorage},
		{name: "TestRedirectTelemetryDoesNotExposeQueryValues", run: TestRedirectTelemetryDoesNotExposeQueryValues},
		{name: "TestSafeTelemetryURLRemovesCredentialsQueryAndFragment", run: TestSafeTelemetryURLRemovesCredentialsQueryAndFragment},
		{name: "TestTimerCrawlRunnerWaiter", run: TestTimerCrawlRunnerWaiter},
		{name: "TestTimerWaitStrategy", run: TestTimerWaitStrategy},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
