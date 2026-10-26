// Package envfile loads a .env file from the current directory into the
// process environment, for development from a checkout. It is deliberately
// narrow: only an allowlist of beacon's own variables is honored (so a .env in
// some unrelated directory can't set PATH or LD_PRELOAD), real environment
// variables always win, and empty values are ignored.
package envfile
