// Package daemon is the engine loop: it watches the state file (and config),
// resolves what the light should show, and applies it. It's the only thing
// that drives the light while running, which is why hooks can just write
// state and exit.
package daemon
