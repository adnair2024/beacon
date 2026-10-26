# Govee API fixtures

- `h612f_devices.json`, `h612f_state.json`: a **real capture** from a Govee Strip
  Light S (H612F), taken with `beacon devices inspect` on 2026-10-25. Device ids
  are redacted (`XX:XX:...`). Notable: 10 segments advertised as
  `size {1,10}` + `elementRange {0,9}` (no `options` list), kelvin 2700-6500, and
  `/device/state` returns `""` for the segment instances (segment state can't be
  read back).
- `docs_shape_devices.json`: **hand-written**, shaped like Govee's documented
  sample (segments listed as `options`). Kept so both shapes stay parsed.
- `variants_devices.json`: invented devices (`TEST-*`) covering non-segmented,
  kelvin-less, white-only, unparseable-segment and unknown-capability cases.

Committed fixtures must have device ids redacted. Capture more with
`beacon devices inspect <name>` (it redacts by default).
