package main

// Build identity is stamped by the image build. Local runs keep these
// defaults so an unstamped binary is obvious in service heartbeats.
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)
