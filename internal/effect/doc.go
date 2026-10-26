// Package effect computes software-driven light effects as pure functions of
// elapsed time. The engine samples Frame and sends the result to a Light, so
// effects work on any backend but only make sense where commands are cheap
// (LAN); the cloud API's rate limits rule them out.
package effect
