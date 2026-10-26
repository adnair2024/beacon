// Package pomo is the Pomodoro state machine. Every function is pure: it
// takes the persisted state and a clock reading and returns new state, so the
// whole cycle is testable without sleeping and any process (CLI, TUI, daemon)
// can catch up from the wall clock after a sleep or restart.
package pomo
