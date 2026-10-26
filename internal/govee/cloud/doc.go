// Package cloud is the Govee Cloud API backend (beacon's primary one). It
// discovers devices through the account and reads each device's SKU and
// capabilities from GET /user/devices at runtime; no model is hard-coded.
//
// Rate limits (per Govee's docs, verify when they change): control is
// 2 req/s per device (burst 6) and 12 req/s per account (burst 80); state is
// 30 req/min per device; the device list is 30 req/min per account. The
// limiters below mirror those, and a 429 triggers a client-wide backoff.
package cloud
