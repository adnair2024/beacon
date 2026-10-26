// Package schedule evaluates a time-of-day lighting schedule and stores it as
// JSON. Evaluation is pure (a profile and a clock reading in, a block out) and
// computes boundaries with time.Date in the profile's zone, so DST is handled
// by the calendar rather than by adding 24 hours.
package schedule
