package auth

import "time"

// Clock abstracts time so tests can inject a fake and avoid real
// wall-clock sleeps in device-flow polling tests.
type Clock interface {
	After(d time.Duration) <-chan time.Time
	Now() time.Time
}

// RealClock delegates to the stdlib — used in production.
type RealClock struct{}

func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (RealClock) Now() time.Time                         { return time.Now() }
