package discovery

import (
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

// CrawlSourceLease is the durable authority token for one discovery crawl.
//
// Generation and ExpiresAt fence renew and complete calls. A crashed claimant
// loses authority when the lease expires so another worker can reclaim the
// source. ClaimedAt is the wall time of the successful claim; it does not
// advance on renew.
type CrawlSourceLease struct {
	Origin     origin.Origin
	Generation int64
	ClaimedAt  time.Time
	ExpiresAt  time.Time
}
