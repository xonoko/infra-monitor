package incident

import "time"

// SetInvestigateRetryWaits shortens the transient-error retry pauses in tests.
func SetInvestigateRetryWaits(w ...time.Duration) { investigateRetryWaits = w }
