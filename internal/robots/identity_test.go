package robots

import (
	"strings"
	"testing"
)

func TestIdentityIsStableAndTransparent(t *testing.T) {
	const wantProductToken = "Joshternet-Joshbot"
	const wantInformationURL = "https://joshternet.org/joshbot"
	const wantUserAgent = "Joshternet-Joshbot " +
		"(+https://joshternet.org/joshbot)"

	if ProductToken != wantProductToken {
		t.Errorf(
			"ProductToken = %q, want %q",
			ProductToken,
			wantProductToken,
		)
	}

	if BotInformationURL != wantInformationURL {
		t.Errorf(
			"BotInformationURL = %q, want %q",
			BotInformationURL,
			wantInformationURL,
		)
	}

	if UserAgent != wantUserAgent {
		t.Errorf(
			"UserAgent = %q, want %q",
			UserAgent,
			wantUserAgent,
		)
	}

	if !strings.Contains(UserAgent, ProductToken) {
		t.Errorf(
			"UserAgent %q does not contain ProductToken %q",
			UserAgent,
			ProductToken,
		)
	}

	if !strings.Contains(UserAgent, BotInformationURL) {
		t.Errorf(
			"UserAgent %q does not contain BotInformationURL %q",
			UserAgent,
			BotInformationURL,
		)
	}
}
