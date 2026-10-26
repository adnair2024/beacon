// Package engine decides what the light should show right now. Resolve is a
// pure function of the clock and the inputs; the (later) loop around it only
// decides when to call it and applies the result.
package engine
