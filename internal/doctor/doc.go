// Package doctor checks a beacon setup end to end and says what to fix. Every
// check is a pure function of Env (config, files, an injected API), so each
// failure mode can be tested without touching the network or the real disk.
package doctor
