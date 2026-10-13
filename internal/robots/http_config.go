package robots

import (
	"errors"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
)

// DefaultMaxCrawlDelay bounds robots-directed spacing independently of the
// operator's minimum request delay.
const DefaultMaxCrawlDelay = 30 * time.Second

// HTTPConfig defines safety limits shared by robots-aware outbound requests.
// Every limit must be positive. Parent request deadlines remain authoritative.
type HTTPConfig struct {
	MaxCrawlDelay         time.Duration
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

// ErrInvalidHTTPConfig reports a non-positive request safety limit.
var ErrInvalidHTTPConfig = errors.New("robots: HTTP limits must be positive")

// DefaultHTTPConfig returns the default outbound request safety limits.
func DefaultHTTPConfig() HTTPConfig {
	return HTTPConfig{
		MaxCrawlDelay:         DefaultMaxCrawlDelay,
		DialTimeout:           netguard.DefaultDialTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

// NewCheckerWithHTTPConfig constructs a guarded checker with explicit limits.
func NewCheckerWithHTTPConfig(
	resolver netguard.Resolver,
	dialer netguard.Dialer,
	requestDelay time.Duration,
	signer RequestSigner,
	config HTTPConfig,
) (*Checker, error) {
	if config.MaxCrawlDelay <= 0 ||
		config.DialTimeout <= 0 ||
		config.TLSHandshakeTimeout <= 0 ||
		config.ResponseHeaderTimeout <= 0 {
		return nil, ErrInvalidHTTPConfig
	}
	if requestDelay < 0 {
		return nil, errInvalidRequestDelay
	}
	return newCheckerWithHTTPConfig(resolver, dialer, requestDelay, signer, config), nil
}

func newCheckerWithHTTPConfig(
	resolver netguard.Resolver,
	dialer netguard.Dialer,
	requestDelay time.Duration,
	signer RequestSigner,
	config HTTPConfig,
) *Checker {
	checker := newCheckerWithRequestDelay(
		&guardedHTTP{resolver: resolver, dialer: dialer, signer: signer, config: config},
		time.Now, requestDelay, timerRequestDelayWaiter{},
	)
	checker.maxCrawlDelay = config.MaxCrawlDelay
	return checker
}
