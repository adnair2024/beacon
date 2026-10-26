# beacon

Govee lights as a desk status beacon: Claude Code status, Pomodoro, and workday schedules in one Go TUI for Linux and Windows.

> **Status: pre-release.** Everything in the feature list below is built and tested, and the Govee side (device listing, capabilities, `set`, segments, the pomodoro bar) has been run on a real account and a real H612F strip. What hasn't happened yet: Claude hooks, the schedule, split mode and autostart have only been exercised against the fake backend and mock servers; nothing has been run on a real Windows machine; and no release has been published. Use `--dry-run` to try everything without a light, and `beacon doctor` to check your own setup.

## Modes

| Mode | What it does |
|---|---|
| **claude** | Shows Claude Code's state on the light: working, ready, waiting for input (via Claude Code hooks). |
| **pomo** | Pomodoro timer with a color per phase. |
| **schedule** | Time-of-day lighting for the workday (on, color shifts, lunch, wind-down, off). |

When several want the light at once, priority is: manual override > claude > pomo > schedule > idle.

## Install

**Releases** (once one is published): download the archive for your OS from the GitHub releases page, unpack it, and put `beacon` (`beacon.exe`) somewhere on your `PATH`. Archives are `beacon_<version>_<os>_<arch>.tar.gz` (Linux amd64/arm64) or `.zip` (Windows amd64), with a `checksums.txt`.

**Arch Linux:** `packaging/aur/PKGBUILD` builds the `beacon-bin` package from a release archive (it also installs a systemd user unit, so `systemctl --user enable --now beacon` works).

**From source** (Go 1.24+, no cgo):

```sh
go install github.com/adnair2024/beacon/cmd/beacon@latest   # or, from a checkout:
go build -o beacon ./cmd/beacon
```

## Quick start

```sh
# 1. A Govee API key: Govee Home app > Profile > Settings > Apply for API Key.
#    Fill in the api_key line at the TOP of the config (it must stay above any [section]).
beacon config edit

# 2. Find your light, then add it to the config as a [[devices]] entry (see below).
beacon devices list
beacon config edit
beacon devices refresh        # caches what the light can do, so hooks are fast
beacon devices test desk      # flashes red, green, blue

# 3. Claude Code status: print the hooks and MERGE them into ~/.claude/settings.json
#    (add to any hooks you already have; don't replace them).
beacon hooks print

# 4. Run the daemon at login, then check everything.
beacon autostart install
beacon doctor

# 5. The terminal UI: dashboard, pomodoro, schedule, colors.
beacon
```

`beacon doctor` is the thing to run whenever something looks wrong; it says what to fix.

## Feature status

- [x] Config: TOML load, defaults, validation (field-named errors), atomic save with `.bak`
- [x] Color parsing: palette names, `#RRGGBB`, `rgb(r,g,b)`, user-defined palette
- [x] `beacon set <color|3000k|on|off> --dry-run`
- [x] TUI shell: Dashboard (live strip, which layer won and why, per-mode summary), Devices, Log, Colors, and an override picker; runs the engine itself, or attaches as a viewer if a daemon is already driving
- [x] TUI **Colors** tab: edit every Claude/Pomo color with a live preview, cycle effects, save
- [x] Software effects: `solid`, `pulse`, `breathe`, `flash`, `rainbow`
- [x] Cloud backend (primary): device SKU and capabilities are read from `/user/devices` at runtime (no model is hard-coded), per-device and per-account rate limiters, 429 backoff, capability checks, redundant-command skipping
- [x] `beacon devices list`, `beacon devices test <name>`, `beacon set --device NAME`
- [x] Command coalescing (rapid state flips collapse into the last one) and multi-device fan-out
- [x] Engine: `Resolve` applies the priority stack (override > claude > pomo > schedule > idle), with override expiry, claude `ready_hold`, and `accent` mode, all table-tested
- [x] Locked, atomically written state file; manual overrides via `beacon set [--for 30m]` and `beacon clear`
- [x] Claude mode: `beacon hook`, session tracking with waiting > working > ready aggregation and TTL pruning, `beacon hooks print`
- [x] `beacon daemon`: file-watching engine loop with a single-instance lock, batching of rapid changes, retry after failed applies, clean shutdown
- [x] Capabilities cache (`devices refresh`) and `devices inspect` with device ids redacted, for capturing test fixtures
- [x] Pomodoro: wall-clock state machine (focus / short / long breaks, pause, skip), `beacon pomo ...`, TUI tab with a big countdown, progress bar, cycle dots, and a phase-end banner; the light shows the phase color, dims when paused, and flashes three times when a phase ends and waits for you
- [x] Schedule: stored as JSON (`schedules/<profile>.json` next to the config), evaluated by weekday and time of day with DST-correct boundaries; `beacon schedule show|next|enable|disable|init|path|validate`; TUI Schedule tab with a table, add/edit/delete, day toggles, a 5-second preview, and live reload by the daemon
- [x] Segments (cloud): `beacon set segments "0-4=orange,5-9=#FF4FA3@30"`; changes are sent as batched, change-only requests; per-device `reverse` / `enabled` / `count` overrides; verified on a real H612F
- [x] Pomodoro progress bar on segmented strips (drains or fills, wakes exactly at each segment boundary), Claude `split` mode with a pulsing "waiting", and `devices test` chasing a segment end to end (milestone 8 complete; the bar and split mode are unit- and daemon-tested, and the bar was run on a real H612F)
- [x] `beacon doctor` (config, key, state, schedule, color clashes, API reachability, per-device capabilities and online state, Claude Code hook installation), `beacon autostart` (systemd user service / Task Scheduler), `beacon config edit`
- [x] Release packaging: GoReleaser config and GitHub Actions (CI and tag-driven releases), a dependency-free `scripts/build-all.sh`, an Arch `beacon-bin` PKGBUILD, version stamping
- [x] Windows pass: retrying atomic file replace (an open handle blocks a rename there), forward-slash hook paths, Task Scheduler autostart. Written and unit-tested from Linux; **not yet run on Windows**
- [x] Every look setting is editable in the TUI (colors, effects, Claude and pomodoro look, idle, named palette)
- ~~LAN backend~~: dropped. Govee's cloud API covers everything beacon needs, and this strip isn't on Govee's LAN-capable list

## Build

Requires Go 1.24+. Pure Go, no cgo.

```sh
go build ./cmd/beacon
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/beacon   # Windows check
```

Release archives for every target (what the release workflow publishes), without needing GoReleaser:

```sh
scripts/build-all.sh                 # version from `git describe`
VERSION=0.2.0 scripts/build-all.sh   # or name it
```

This writes `dist/beacon_<version>_<os>_<arch>.{tar.gz,zip}` and `dist/checksums.txt`. Pushing a tag like `v0.2.0` runs `.github/workflows/release.yml`, which runs the tests and then GoReleaser (`.goreleaser.yaml`) to create a **draft** GitHub release for you to review and publish. `beacon version` shows the version, commit and date the binary was built from.

## Try it

```sh
./beacon                          # open the TUI
./beacon --dry-run                # TUI with a fake strip and its own throwaway state
./beacon devices list --dry-run                      # fake devices with different capabilities
./beacon set orange --device desk --dry-run
./beacon set 3000k --device shelf --dry-run
./beacon set red --device shelf --dry-run            # refused: that fake device has no color
./beacon devices test desk --dry-run
./beacon config path
./beacon config validate
```

### The TUI

`beacon` (or `beacon tui`) opens the TUI. Who drives the light depends on what's running:

| Mode | When | What it means |
|---|---|---|
| **engine** | nothing else is running and a device resolves | the TUI runs the engine loop itself and drives the light; quitting stops the engine but leaves the light as it is |
| **viewer** | a `beacon daemon` (or another TUI) holds the engine lock | the TUI shows exactly what the daemon is doing; overrides and clears you make are sent through the state file |
| **offline** | no API key or no device | nothing is sent to a light, but the dashboard still shows what the engine *would* do and you can edit colors |

Tabs: `1` Dashboard, `2` Pomo, `3` Schedule, `4` Devices, `5` Log, `6` Colors.

| Key | Action |
|---|---|
| `1`-`6`, `tab` / `shift+tab` | switch tabs |
| `o` | override picker: a color, `3000k`, `on` or `off` (`tab` completes palette names), plus brightness; lasts `override.default_duration` |
| `c` | clear the override |
| `?` | toggle full help |
| `q` / `ctrl+c` | quit |

On the Pomo tab: `space` starts (idle, or when a finished phase is waiting for you), pauses, or resumes; `s` skips to the next phase; `x` stops.

On the Schedule tab: `↑`/`↓` select, `a` add, `e` edit (fields: time, name, power, light, brightness; blank means inherit), `d` delete (asks first), `t` toggle weekdays, `p` preview the row on the light for 5 seconds, `E` enable/disable the whole schedule. Every confirmed change is written straight to the JSON file.

On the Colors tab, **every setting that changes how the light looks** is editable, in sections that scroll: the Claude colors (working, ready, waiting) and effects, Claude's mode, split segments and brightness; the pomodoro colors, effects, progress style (bar or solid), direction and the unfilled-bar dim level; the idle action and idle color; and your **named palette**. `↑`/`↓` select, `enter` edits (a color takes a name, `#RRGGBB` or `rgb(r,g,b)` and `tab` cycles the known names; a number is range-checked; a choice just cycles), `e` cycles an effect or a choice (`←` goes back), `a` adds a palette color (name it, then give it a value), `d` deletes one (asks first), `s` saves to the config file, and `esc` cancels. Palette names work anywhere a color does, including schedule entries and the roles above, immediately. Saving is refused, with the reason, if something wouldn't be valid (for example an idle action of "color" with no idle color, or deleting a palette color a role still uses). A running daemon or TUI picks the saved colors up on its own. The preview animates the selected effect as it would be sent to a light. The Log tab follows `beacon.log` (`G` jumps to the end).

The dashboard's strip draws one cell per segment on segmented devices (the count comes from the API), or a single swatch otherwise. With `NO_COLOR` set it becomes text. The minimum terminal size is 60x18. The accent color of the whole UI follows the light's current color.

## Using a real light

1. Create a Govee API key (Govee Home app: Profile > Settings > Apply for API Key) and give it to beacon in **one** of these ways (first match wins):
   - the `GOVEE_API_KEY` environment variable;
   - a `.env` file in the directory you run beacon from (handy from a checkout): `cp .env.example .env && chmod 600 .env`, then fill in `GOVEE_API_KEY=`. `.env` is git-ignored; `.env.example` documents the format and is the only env file that is committed. Only `GOVEE_API_KEY`, `BEACON_CONFIG` and `BEACON_STATE_DIR` are read from it, real environment variables win over it, and empty values are ignored;
   - `api_key = "..."` in `config.toml` (created `0600`), which works from any directory.

   `beacon config validate` tells you which one it used, with the key masked (`****abcd`), and warns if `.env` is readable by other users. Never commit or share the key.
2. `beacon devices list` shows every light on the account with the SKU and capabilities Govee reports for it.
3. `beacon devices test <name>` flashes red, green, blue.
4. `beacon set <color|3000k|on|off> [--brightness N] [--for 30m]` sets a manual override, the highest-priority layer. It lasts `override.default_duration` (15m) unless you pass `--for`; `--for 0` keeps it until `beacon clear`. `--device NAME` is a one-off poke at one device and records no override.
5. `beacon clear` cancels the override and applies whatever the next layer wants.

> Overrides expire on their own only while `beacon daemon` (or the TUI) is running; without it an expired override is just ignored the next time beacon runs.

With one device on the account, `--device` is optional. With several, either pass `--device` or list the ones to control in the config:

```toml
[[devices]]
name = "desk"                          # your nickname
id = "AA:BB:CC:DD:EE:FF:00:01"         # from `beacon devices list`
backend = "cloud"
```

beacon never assumes a model. If you ask a device for something it doesn't advertise (a color on a white-only light, a kelvin outside its range), the command is refused up front and nothing is sent.

## Schedule

The schedule sets the light by time of day on the weekdays you choose. It is stored as **JSON**, not in `config.toml`, so the TUI editor can rewrite it freely (TOML rewriting would eat your comments) and you can still edit it by hand. `config.toml` only names which schedule is active:

```toml
[schedule]
profile = "workday"     # -> schedules/workday.json, next to config.toml
```

Until that file exists the shipped workday schedule is used (arrive 07:55 warm and dim, focus 09:30, amber lunch 11:00, back to work 12:00, wind down 14:30, off 16:15, weekdays). `beacon schedule init` writes it out for editing; the first change from the TUI also creates it. `beacon schedule path` prints where it is.

```json
{
  "version": 1,
  "enabled": true,
  "days": ["mon", "tue", "wed", "thu", "fri"],
  "timezone": "",
  "entries": [
    { "at": "07:55", "name": "Arrive", "power": true, "kelvin": 2900, "brightness": 60 },
    { "at": "11:00", "name": "Lunch", "color": "amber", "brightness": 25 },
    { "at": "16:15", "name": "Leave", "power": false }
  ]
}
```

- **Entries are state, not events.** Start beacon at 10:00 and the 09:30 entry is already in force. Fields you leave out inherit from the previous entry that day. `color` and `kelvin` replace each other. `color` takes a palette name, `#RRGGBB`, or `rgb(r,g,b)`.
- **`power: false` ends the day** for this layer: the light is turned off and later entries that day are ignored.
- **Before the first entry, or on a day that isn't listed, the schedule contributes nothing**, and the light falls through to the idle setting.
- **Time zones and DST:** `timezone` is an IANA name (e.g. `America/Chicago`); empty means the system's. Times are computed from the calendar, not by adding 24 hours. An entry whose time doesn't exist on a spring-forward day (02:30) moves forward by the gap (to 03:30); an hour that repeats in the fall runs once.
- **It lives below the Claude status, the pomodoro and manual overrides**, and above idle.
- **Live reload:** the daemon / TUI watch the schedules directory. Saving the file changes the light within a moment; a half-saved or invalid file is ignored (the last good schedule stays in force) and logged once. `beacon schedule validate` and `beacon config validate` report problems with the file, JSON syntax errors with line and column.
- `--dry-run` uses its own throwaway `schedules/.dry-run/` copy, so it never rewrites your real schedule.
- It only runs while `beacon daemon` or the TUI is running; beacon does not program Govee's on-device schedules.

## Claude split mode

`claude.mode = "split"` keeps the pomodoro (or the schedule) visible while Claude works: Claude owns the first `claude.split_segments` segments (default 2) and the rest of the strip shows whatever is below it. While a prompt is **waiting** for you, Claude's segments pulse between full and 30% every 2 seconds for a minute (one small request per step), so a permission prompt is noticeable on a short slice of the strip. Details:

- The pomodoro bar always uses only the segments after Claude's, even when no session is active, so it doesn't reflow when one starts; Claude's segments show the phase color then.
- If the strip has no segments, or there'd be none left for the rest, split mode behaves exactly like `override` (Claude takes the whole strip).
- If the layer below wants the light off (e.g. after the schedule's power-off), Claude's segments still light and everything else stays dark.
- A manual override still beats everything, split mode included.

## Segments

If the API says a device has segments (the H612F reports 10, and per-segment brightness), you can address them:

```sh
beacon set segments "0-4=orange,5-9=#FF4FA3@30"   # @N = that group's brightness (1-100)
beacon set segments "0=red" --device desk          # a one-off, no override recorded
```

Segment 0 is the first one the API numbers; on the strip tested it is the end the cable leaves from. If yours is mounted the other way round, flip the numbering without touching anything else:

```toml
[[devices]]
name = "desk"
[devices.segments]
reverse = true     # segment 0 <-> last, applied only when talking to the API
# enabled = false  # ignore segments for this device
# count = 10       # override a bad parse of the API's segment count
```

How beacon talks to the API about segments:
- **Batched and change-only:** one request per distinct color and one per distinct brightness, and only for segments that changed. Moving a progress bar by one segment is one request.
- **Segment state can't be read back** (`/device/state` returns nothing for it), so beacon remembers what it last sent. After any error it forgets and resends everything.
- **A whole-strip color or brightness command resets the segments**, so beacon sends those first and resends the segments after them. Going from segments back to a plain color forces the whole-strip command even if the value looks unchanged.
- Devices without segments get the first segment's color as a whole-strip color instead of an error, unless they can't show color at all.

## Pomodoro

`beacon pomo start | pause | resume | skip | stop | status` (or the Pomo tab). Defaults: 25 minute focus, 5 minute short break, 15 minute long break after every 4 focus blocks; breaks start on their own, the next focus block waits for you (`pomo.auto_start_breaks` / `pomo.auto_start_focus`).

- **On the light:** focus pink, short break teal, long break purple (all configurable on the Colors tab). Paused shows the phase color at 20%. When a phase ends and is waiting on you, the light flashes three times and then glows dimly in the color of what's next.
- **Wall clock, not ticks:** the timer is stored as start/end times, so it keeps counting while beacon isn't running, survives sleep and restarts, and phases never drift. Whoever looks next (CLI, TUI, daemon) catches the state up; with the daemon running, phase changes reach the light exactly on the boundary.
- **Skipping** a focus block starts a break but earns no credit toward the long-break cadence.
- **Stopping** leaves the light as it is (like `beacon clear`) unless nothing else wants it and `idle.action = "off"`.
- **Priority:** a working/waiting Claude session or a manual override takes the light over the timer; the timer keeps counting underneath.
- **Progress bar (segmented strips):** with `pomo.progress = "bar"` (the default) and a device that advertises segments, the running timer is a bar instead of a solid color: every segment lit in the phase color at the start, then one segment dims every `length / segments` (2m30s for a 25-minute focus block over 10 segments), starting from the far end so the lit part retracts toward segment 0. Dimmed segments sit at `pomo.progress_dim` percent (default 20). `pomo.progress_direction = "fill"` runs it the other way. Paused shows the same bar at 20%. Devices without segments (or `progress = "solid"`) get the solid color. The daemon wakes exactly at each boundary and sends one request for the one segment that changed.
- Over the cloud API each phase change is one or two commands, which is well inside the rate limits. The three-flash effect on a phase end sends about six brightness commands over four seconds.

## Claude Code status on the light

1. `beacon hooks print` prints a `hooks` block for `~/.claude/settings.json` (the VS Code extension reads the same file). **Merge it into your existing hooks; don't replace the file.** If you already have, say, a `PreToolUse` hook, beacon's entries go *alongside* it in the same list. `beacon doctor` checks the result. The hooks:

   | Hook | Light | Why |
   |---|---|---|
   | `SessionStart` | ready | a new session starts out ready |
   | `UserPromptSubmit` | working | you sent a prompt |
   | `Notification` | waiting | **only** for `permission_prompt`, `idle_prompt`, `elicitation_dialog` and `agent_needs_input` (a matcher; Notification also fires for things like `auth_success` that don't need you) |
   | `Stop` | ready | Claude finished |
   | `StopFailure` | ready | a turn that ends on an API error never fires `Stop`; without this the light would stay on "working" until the session times out (`--no-stop-failure` leaves it out for an older Claude Code) |
   | `SessionEnd` | (session removed) | |
   | `PreToolUse` | working | opt-in with `--pre-tool-use`: re-asserts "working" after you answer a permission prompt, at the cost of one beacon process per tool call |

   The hooks use the full path of the beacon binary, since the shell Claude Code uses may not have it on `PATH`. For Windows, `--windows [--path 'C:\...\beacon.exe']` writes forward slashes (bash treats backslashes as escapes) and quotes paths with spaces.
2. Run `beacon daemon` (or the TUI) and leave it running; `beacon autostart install` does that at login. Hooks then only record the event and return in a couple of milliseconds; the daemon drives the light.
3. Colors: working orange, waiting blue, ready green (change them on the TUI Colors tab). Any session waiting wins over working, which wins over ready. After everything is ready the light holds green for `claude.ready_hold` (10s), then hands the light back to the next layer. Sessions that stop reporting (a crashed Claude) are dropped after `claude.session_ttl` (30m).

**Without the daemon** a hook applies the change itself. Over the cloud API that costs a network round trip, so it cannot meet the 150 ms target (hooks are given a 2 s budget instead); run the daemon. A hook always exits 0 and prints nothing to stdout, so it can't disrupt Claude Code; problems go to `beacon.log` in the state directory, which never contains hook payloads, only the event name and session id.

Hooks use the capabilities cached in the config (`beacon devices refresh`) so they don't need a `/user/devices` call; without a cache they fall back to listing the account.

## Autostart

`beacon autostart install` runs `beacon daemon` at login and starts it now: a **systemd user service** on Linux (`~/.config/systemd/user/beacon.service`), a **Task Scheduler** task named `beacon` on Windows (hidden PowerShell window, at logon). `beacon autostart status` and `beacon autostart remove` do what they say; `install --print` shows what would be installed and changes nothing.

A daemon started at login doesn't see your shell, so **the API key has to be in `config.toml`** (or in a `.env` in the directory you install from, in which case the service starts in that directory). Installing with the key only in `GOVEE_API_KEY` is refused with an explanation. The key is never written into the service definition.

## Troubleshooting: `beacon doctor`

`beacon doctor` checks, and for each problem says what to run:

- the config (parse and validation errors by field), `.env` problems, and where the API key comes from;
- that the state directory is writable, the schedule file is valid, and no two status colors are identical or look alike;
- Govee: that the API is reachable (and which single host to allow on a locked-down network), that the key is accepted, that each configured device exists, its cached capabilities are current, and it's online (an offline light gets the 2.4 GHz / WPA2-Personal / no-captive-portal checklist);
- Claude Code: that the required hooks are installed in your settings (user and project files), that `Notification` has its matcher, that `disableAllHooks` isn't set, and that the hook commands point at a binary that exists and is the one you're running;
- whether a daemon is running.

It exits non-zero only for real problems (warnings don't fail it). `--offline` skips everything that talks to Govee; `--dry-run` checks against the fake devices.

## Windows

- Config is `%AppData%\beacon\config.toml`, state and logs are in `%LocalAppData%\beacon\`, schedules in `schedules\` next to the config. Nothing needs a firewall exception: beacon only makes outbound HTTPS requests to `openapi.api.govee.com`.
- Generate the hooks with `beacon hooks print --windows` (forward-slash paths). Use `beacon autostart install` for the Task Scheduler entry (it needs the key in `config.toml` or a user-level `GOVEE_API_KEY` environment variable; a `.env` isn't seen by a login task).
- The TUI is built for Windows Terminal and conhost on Windows 10 (no emoji in essential UI, color falls back automatically, `NO_COLOR` gives a text-only strip), but that is the design, not something that's been observed yet.
- Saving a file replaces it atomically; on Windows another process holding the old file open blocks that, so beacon retries for about half a second before giving up.
- All of this has been compiled and unit-tested from Linux but **not run on a Windows machine yet**. If something misbehaves there, `beacon doctor` output is the most useful thing to include in a report.

## Configuration

Config lives at `~/.config/beacon/config.toml` on Linux and `%AppData%\beacon\config.toml` on Windows (`BEACON_CONFIG` overrides it). A missing file means defaults. Saving from the TUI rewrites the file, which drops comments; the previous version is kept as `config.toml.bak`.

```toml
[claude]
working = "#FF8C00"
ready   = "#00C853"
waiting = "#2979FF"

[effects]            # solid | pulse | breathe | flash | rainbow
claude_working = "pulse"
claude_waiting = "flash"
```

The Govee API key is read from `GOVEE_API_KEY` first, then `api_key` in the config file. It is never printed in full.

### Effects

Effects are computed in beacon and sent to the light as a stream of brightness/color updates. Govee's Cloud API allows about 2 control requests per second per device (burst 6), so animated effects over the cloud would be a slideshow and would trip the limiter. Beacon has no LAN backend (Govee's cloud API covers what it needs), so treat effects as a preview in the TUI: the engine drives cloud devices with a solid color.

## Notes

- The schedule runs inside beacon. It does not program Govee's on-device schedules, so it only works while beacon is running.
- beacon talks only to your Govee devices and `openapi.api.govee.com`. No telemetry, no update checks.
- Everything goes through Govee's cloud API, which works across isolated IoT networks and VLANs with no discovery. There is deliberately no LAN backend, so nothing on your network needs to reach the light.
