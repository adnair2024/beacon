package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/envfile"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/cloud"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

// Status is how a check turned out.
type Status int

const (
	OK   Status = iota
	Info        // worth knowing, nothing to fix
	Warn        // works, but will bite later
	Fail        // something is broken
)

// Tag is the label printed for a status (plain ASCII: it must read on any console).
func (s Status) Tag() string { return [...]string{"ok", "info", "warn", "FAIL"}[s] }

// Label is Tag padded to four columns, so "[ ok ]" lines up with "[warn]".
func (s Status) Label() string { return [...]string{" ok ", "info", "warn", "FAIL"}[s] }

// Result is one line of the report.
type Result struct {
	Name   string
	Status Status
	Detail string
	Hints  []string // what to run or change, one per line
}

// SettingsFile is a Claude Code settings file as found on disk.
type SettingsFile struct {
	Path    string
	Exists  bool
	Data    []byte
	ReadErr error
}

// Env is everything the checks look at. The function fields are injected so
// tests need no network and no real filesystem.
type Env struct {
	Cfg      config.Config
	CfgPath  string
	CfgFound bool
	CfgErr   error // the config failed to parse
	Warnings []string

	EnvFile   envfile.Result
	KeySource string // "from .env", "from the GOVEE_API_KEY environment variable", "from config.toml" or ""
	KeyMasked string

	StateDir  string
	Store     *state.Store
	SchedDir  string
	Offline   bool
	Exe       string // this binary's path
	GOOS      string
	Claude    []SettingsFile
	List      func(ctx context.Context) ([]govee.Device, error)
	Online    func(ctx context.Context, d govee.Device) error // nil when online, cloud.ErrOffline when not
	LookPath  func(string) (string, error)
	Stat      func(string) (os.FileInfo, error)
	Timeout   time.Duration
	Now       func() time.Time
	RoundTrip time.Duration // filled in by the API check
}

func (e Env) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return 10 * time.Second
}

// Run executes every check in a sensible order.
func Run(ctx context.Context, e Env) []Result {
	if e.Now == nil {
		e.Now = time.Now
	}
	var out []Result
	out = append(out, checkConfig(e)...)
	out = append(out, checkDotenv(e)...)
	out = append(out, checkKey(e))
	out = append(out, checkState(e)...)
	out = append(out, checkSchedule(e)...)
	out = append(out, checkColors(e)...)
	if e.Offline {
		out = append(out, Result{Name: "api", Status: Info, Detail: "skipped (--offline)"})
	} else {
		out = append(out, checkAPI(ctx, &e)...)
	}
	out = append(out, checkHooks(e)...)
	out = append(out, checkEngine(e))
	return out
}

// Summary counts problems for the exit status and the closing line.
func Summary(rs []Result) (fails, warns int) {
	for _, r := range rs {
		switch r.Status {
		case Fail:
			fails++
		case Warn:
			warns++
		}
	}
	return
}

// ---- config ----

func checkConfig(e Env) []Result {
	switch {
	case e.CfgErr != nil:
		return []Result{{Name: "config", Status: Fail, Detail: e.CfgErr.Error(),
			Hints: []string{"fix the file, or move it aside to start from the defaults: beacon config path"}}}
	case !e.CfgFound:
		return []Result{{Name: "config", Status: Warn, Detail: "no config file at " + e.CfgPath + " (using defaults)",
			Hints: []string{"beacon works without one, but add your light with a [[devices]] entry and run `beacon devices refresh` so hooks don't have to look devices up over the network every time"}}}
	}
	var out []Result
	if errs := e.Cfg.Validate(); len(errs) > 0 {
		r := Result{Name: "config", Status: Fail, Detail: fmt.Sprintf("%s has %d problem(s)", e.CfgPath, len(errs))}
		for i, err := range errs {
			if i == 5 {
				r.Hints = append(r.Hints, fmt.Sprintf("...and %d more (beacon config validate lists them all)", len(errs)-i))
				break
			}
			r.Hints = append(r.Hints, err.Error())
		}
		return []Result{r}
	}
	out = append(out, Result{Name: "config", Status: OK, Detail: fmt.Sprintf("%s (%d device%s configured)", e.CfgPath, len(e.Cfg.Devices), plural(len(e.Cfg.Devices)))})
	for _, w := range e.Warnings {
		out = append(out, Result{Name: "config", Status: Warn, Detail: w, Hints: []string{"unknown keys are ignored, but a typo means the setting you meant isn't applied"}})
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func checkDotenv(e Env) []Result {
	r := e.EnvFile
	if r.Path == "" || (len(r.Set) == 0 && len(r.Skipped) == 0 && len(r.Errors) == 0) {
		return nil // no .env in use: nothing to say
	}
	var out []Result
	for _, err := range r.Errors {
		out = append(out, Result{Name: ".env", Status: Warn, Detail: err.Error(), Hints: []string{"see .env.example for the format"}})
	}
	if len(r.Skipped) > 0 {
		out = append(out, Result{Name: ".env", Status: Warn, Detail: "ignored variables beacon doesn't read: " + strings.Join(r.Skipped, ", "),
			Hints: []string{"only " + strings.Join(envfile.Allowed, ", ") + " are read from .env"}})
	}
	if envfile.TooOpen(r.Path) {
		out = append(out, Result{Name: ".env", Status: Warn, Detail: "readable by other users", Hints: []string{"chmod 600 " + r.Path}})
	}
	if len(out) == 0 {
		out = append(out, Result{Name: ".env", Status: OK, Detail: "loaded " + strings.Join(r.Set, ", ")})
	}
	return out
}

func checkKey(e Env) Result {
	if e.KeySource == "" {
		return Result{Name: "api key", Status: Fail, Detail: "not set",
			Hints: []string{
				"get one in the Govee Home app: Profile > Settings > Apply for API Key",
				"then set GOVEE_API_KEY, put it in a .env file (see .env.example), or add api_key = \"...\" to " + e.CfgPath,
			}}
	}
	return Result{Name: "api key", Status: OK, Detail: fmt.Sprintf("%s (%s)", e.KeySource, e.KeyMasked)}
}

// ---- state, schedule ----

func checkState(e Env) []Result {
	var out []Result
	if e.StateDir == "" {
		return []Result{{Name: "state", Status: Fail, Detail: "no state directory could be determined"}}
	}
	// Writable? A hook that can't write state silently does nothing.
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return []Result{{Name: "state", Status: Fail, Detail: "can't create " + e.StateDir + ": " + err.Error(),
			Hints: []string{"hooks and the daemon keep their state there; set BEACON_STATE_DIR to somewhere writable"}}}
	}
	probe, err := os.CreateTemp(e.StateDir, ".doctor-*")
	if err != nil {
		return []Result{{Name: "state", Status: Fail, Detail: e.StateDir + " is not writable: " + err.Error(),
			Hints: []string{"hooks and the daemon keep their state there; set BEACON_STATE_DIR to somewhere writable"}}}
	}
	probe.Close()
	os.Remove(probe.Name())
	out = append(out, Result{Name: "state", Status: OK, Detail: e.StateDir + " (writable)"})
	if _, err := os.Stat(filepath.Join(e.StateDir, "state.json.corrupt")); err == nil {
		out = append(out, Result{Name: "state", Status: Warn, Detail: "an unreadable state.json was set aside as state.json.corrupt",
			Hints: []string{"a hand edit or a crash mid-write did this; beacon started over cleanly. Delete the .corrupt file once you've looked at it."}})
	}
	if e.Store != nil {
		if _, err := e.Store.Load(); err != nil && !errors.Is(err, state.ErrCorrupt) {
			out = append(out, Result{Name: "state", Status: Fail, Detail: err.Error()})
		}
	}
	return out
}

func checkSchedule(e Env) []Result {
	if e.SchedDir == "" || !schedule.ValidName(e.Cfg.Schedule.Profile) {
		return nil // an invalid profile name is already reported by the config check
	}
	p, exists, err := schedule.Load(e.SchedDir, e.Cfg.Schedule.Profile)
	name := e.Cfg.Schedule.Profile + ".json"
	switch {
	case err != nil:
		return []Result{{Name: "schedule", Status: Fail, Detail: err.Error(),
			Hints: []string{"fix the JSON, or delete the file to go back to the built-in schedule (beacon keeps the last good one while it runs)"}}}
	}
	if errs := p.Validate(e.Cfg.UserPalette()); len(errs) > 0 {
		r := Result{Name: "schedule", Status: Fail, Detail: fmt.Sprintf("%s has %d problem(s)", name, len(errs))}
		for _, err := range errs {
			r.Hints = append(r.Hints, err.Error())
		}
		return []Result{r}
	}
	src := name
	if !exists {
		src = "built-in default (no " + name + " yet)"
	}
	switch {
	case !p.IsEnabled():
		return []Result{{Name: "schedule", Status: Info, Detail: src + ": disabled"}}
	case len(p.Days) == 0:
		return []Result{{Name: "schedule", Status: Warn, Detail: src + ": no days are enabled, so it never does anything",
			Hints: []string{"toggle days on the Schedule tab (t), or disable the schedule"}}}
	}
	return []Result{{Name: "schedule", Status: OK, Detail: fmt.Sprintf("%s: %d entries on %d days", src, len(p.Entries), len(p.Days))}}
}

// ---- colors ----

type meaning struct {
	name string
	rgb  color.RGB
}

// colorDistance is the Euclidean distance between two colors in RGB space.
func colorDistance(a, b color.RGB) float64 {
	dr, dg, db := float64(a.R)-float64(b.R), float64(a.G)-float64(b.G), float64(a.B)-float64(b.B)
	return sqrt(dr*dr + dg*dg + db*db)
}

func sqrt(x float64) float64 { // Newton's method; avoids importing math for one call
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 30; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// similar is how close two colors can be before they're hard to tell apart on
// a light across a room.
const similar = 60.0

func checkColors(e Env) []Result {
	pal := e.Cfg.UserPalette()
	var ms []meaning
	add := func(name, v string) {
		if c, err := color.Parse(v, pal); err == nil {
			ms = append(ms, meaning{name, c})
		}
	}
	add("claude.working", e.Cfg.Claude.Working)
	add("claude.ready", e.Cfg.Claude.Ready)
	add("claude.waiting", e.Cfg.Claude.Waiting)
	add("pomo.focus", e.Cfg.Pomo.FocusColor)
	add("pomo.short_break", e.Cfg.Pomo.ShortBreakColor)
	add("pomo.long_break", e.Cfg.Pomo.LongBreakColor)
	if e.Cfg.Idle.Action == "color" {
		add("idle", e.Cfg.Idle.Color)
	}
	var out []Result
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			d := colorDistance(ms[i].rgb, ms[j].rgb)
			switch {
			case d == 0:
				out = append(out, Result{Name: "colors", Status: Warn,
					Detail: fmt.Sprintf("%s and %s are the same color (%s)", ms[i].name, ms[j].name, ms[i].rgb.Hex()),
					Hints:  []string{"you won't be able to tell them apart on the light; change one on the Colors tab"}})
			case d < similar:
				out = append(out, Result{Name: "colors", Status: Warn,
					Detail: fmt.Sprintf("%s (%s) and %s (%s) are very similar", ms[i].name, ms[i].rgb.Hex(), ms[j].name, ms[j].rgb.Hex()),
					Hints:  []string{"they may be hard to tell apart from across a room"}})
			}
		}
	}
	if len(out) == 0 {
		out = append(out, Result{Name: "colors", Status: OK, Detail: fmt.Sprintf("%d status colors, all distinct", len(ms))})
	}
	return out
}

// ---- API and devices ----

func checkAPI(ctx context.Context, e *Env) []Result {
	if e.KeySource == "" {
		return []Result{{Name: "api", Status: Info, Detail: "skipped (no API key)"}}
	}
	if e.List == nil {
		return []Result{{Name: "api", Status: Info, Detail: "skipped"}}
	}
	cctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	start := e.Now()
	devs, err := e.List(cctx)
	e.RoundTrip = e.Now().Sub(start)
	var rl *cloud.RateLimitError
	switch {
	case errors.Is(err, cloud.ErrUnauthorized):
		return []Result{{Name: "api", Status: Fail, Detail: "Govee rejected the API key",
			Hints: []string{"check it was copied whole, or apply for a new one in the Govee Home app (Profile > Settings > Apply for API Key)"}}}
	case errors.As(err, &rl):
		return []Result{{Name: "api", Status: Warn, Detail: err.Error(), Hints: []string{"Govee limits requests; wait a minute and run doctor again"}}}
	case err != nil:
		return []Result{{Name: "api", Status: Fail, Detail: err.Error(),
			Hints: []string{
				"beacon only needs HTTPS to openapi.api.govee.com; check your connection, firewall or proxy",
				"on a restricted network, ask for that one host to be allowed",
			}}}
	}
	out := []Result{{Name: "api", Status: OK, Detail: fmt.Sprintf("reached Govee: %d device%s on the account (%d ms)", len(devs), plural(len(devs)), e.RoundTrip.Milliseconds())}}
	return append(out, checkDevices(ctx, *e, devs)...)
}

func lookup(devs []govee.Device, id, name string) (govee.Device, bool) {
	for _, d := range devs {
		if (id != "" && strings.EqualFold(d.ID, id)) || (name != "" && strings.EqualFold(d.Name, name)) {
			return d, true
		}
	}
	return govee.Device{}, false
}

func checkDevices(ctx context.Context, e Env, listed []govee.Device) []Result {
	var out []Result
	if len(e.Cfg.Devices) == 0 {
		switch len(listed) {
		case 0:
			return []Result{{Name: "devices", Status: Fail, Detail: "no devices on this account",
				Hints: []string{"set the light up in the Govee Home app first (Matter alone isn't enough: it has to be in the app for the API to list it)"}}}
		case 1:
			return []Result{{Name: "devices", Status: Info, Detail: fmt.Sprintf("none configured; beacon will use the only one on the account, %q (%s)", listed[0].Name, listed[0].SKU),
				Hints: []string{"add it as a [[devices]] entry and run `beacon devices refresh` so hooks can skip the network lookup"}}}
		}
		return []Result{{Name: "devices", Status: Fail, Detail: fmt.Sprintf("%d devices on the account and none configured, so beacon can't tell which to drive", len(listed)),
			Hints: []string{"run `beacon devices list`, then add the one(s) you want as [[devices]] in the config"}}}
	}
	for _, c := range e.Cfg.Devices {
		d, ok := lookup(listed, c.ID, c.Name)
		if !ok {
			out = append(out, Result{Name: "device " + c.Name, Status: Fail, Detail: "isn't on this Govee account",
				Hints: []string{"run `beacon devices list` and fix the id/name in [[devices]]"}})
			continue
		}
		out = append(out, deviceChecks(ctx, e, c, d)...)
	}
	return out
}

func deviceChecks(ctx context.Context, e Env, c config.Device, d govee.Device) []Result {
	name := "device " + c.Name
	var out []Result
	switch {
	case c.Caps == nil:
		out = append(out, Result{Name: name, Status: Warn, Detail: fmt.Sprintf("%s: capabilities aren't cached", d.SKU),
			Hints: []string{"without the cache every hook has to list devices over the network first; run: beacon devices refresh"}})
	case *c.Caps != config.CacheOf(d.Caps) || c.SKU != d.SKU:
		out = append(out, Result{Name: name, Status: Warn, Detail: "the cached capabilities are out of date",
			Hints: []string{"run: beacon devices refresh"}})
	default:
		out = append(out, Result{Name: name, Status: OK, Detail: fmt.Sprintf("%s: %s", d.SKU, d.Caps)})
	}
	if d.Caps.Segments {
		switch {
		case !c.SegmentsEnabled():
			out = append(out, Result{Name: name, Status: Info, Detail: fmt.Sprintf("has %d segments but they're switched off for it (devices.segments.enabled = false)", d.Caps.SegmentCount)})
		case c.Segments.Count > 0 && c.Segments.Count != d.Caps.SegmentCount:
			out = append(out, Result{Name: name, Status: Warn, Detail: fmt.Sprintf("devices.segments.count = %d overrides what the API reports (%d)", c.Segments.Count, d.Caps.SegmentCount),
				Hints: []string{"remove the override unless the API's number is wrong"}})
		}
	}
	if e.Online != nil {
		cctx, cancel := context.WithTimeout(ctx, e.timeout())
		err := e.Online(cctx, d)
		cancel()
		switch {
		case errors.Is(err, cloud.ErrOffline):
			out = append(out, Result{Name: name, Status: Warn, Detail: "Govee reports it offline",
				Hints: []string{
					"Govee lights need a 2.4 GHz Wi-Fi network with plain WPA2/WPA3-Personal (no captive portal, no 802.1X)",
					"power-cycle it, and check it's online in the Govee Home app",
				}})
		case err != nil:
			out = append(out, Result{Name: name, Status: Warn, Detail: "couldn't read its state: " + err.Error()})
		}
	}
	return out
}

// ---- Claude Code hooks ----

func checkHooks(e Env) []Result {
	var out []Result
	var all []claude.Found
	var checked []string
	for _, f := range e.Claude {
		checked = append(checked, f.Path)
		if !f.Exists {
			continue
		}
		if f.ReadErr != nil {
			out = append(out, Result{Name: "hooks", Status: Warn, Detail: "couldn't read " + f.Path + ": " + f.ReadErr.Error()})
			continue
		}
		found, err := claude.ParseSettings(f.Data)
		if err != nil {
			out = append(out, Result{Name: "hooks", Status: Fail, Detail: f.Path + ": " + err.Error(),
				Hints: []string{"Claude Code ignores a settings file it can't parse, hooks included"}})
			continue
		}
		if disabled(f.Data) {
			out = append(out, Result{Name: "hooks", Status: Fail, Detail: f.Path + " sets disableAllHooks, so no hook runs",
				Hints: []string{"remove \"disableAllHooks\" from that file"}})
		}
		all = append(all, found...)
	}

	// Which of beacon's hooks are present?
	// A hook is beacon's if the binary is named beacon or is this very binary
	// (it may have been renamed).
	ours := func(bin string) bool { return e.Exe != "" && sameFile(bin, e.Exe) }
	have := map[string][]claude.Found{}
	for _, f := range all {
		if bin, ev, ok := f.BeaconWith(ours); ok && bin != "" {
			have[ev+"|"+f.Hook] = append(have[ev+"|"+f.Hook], f)
		}
	}
	anyBeacon := len(have) > 0
	if !anyBeacon {
		out = append(out, Result{Name: "hooks", Status: Fail, Detail: "no beacon hooks in Claude Code's settings (checked: " + strings.Join(checked, ", ") + ")",
			Hints: []string{
				"print them with: beacon hooks print",
				"and merge the \"hooks\" block into ~/.claude/settings.json, adding to any hooks you already have rather than replacing them",
			}})
		return out
	}
	missingRequired := false
	for _, s := range claude.Specs() {
		fs := have[s.Event+"|"+s.Hook]
		if len(fs) == 0 {
			switch s.Level {
			case claude.Required:
				missingRequired = true
				out = append(out, Result{Name: "hooks", Status: Fail, Detail: "missing the " + s.Hook + " hook: " + s.Why,
					Hints: []string{"beacon hooks print shows the full set"}})
			case claude.Recommended:
				out = append(out, Result{Name: "hooks", Status: Warn, Detail: "missing the " + s.Hook + " hook: " + s.Why,
					Hints: []string{"beacon hooks print includes it"}})
			}
			continue
		}
		if s.Matcher != "" && !matcherOK(fs, s.Matcher) {
			out = append(out, Result{Name: "hooks", Status: Warn,
				Detail: s.Hook + " has no matcher, so it also fires for notifications that don't need you (auth_success, agent_completed, ...) and turns the light blue for no reason",
				Hints:  []string{"use the matcher: " + s.Matcher}})
		}
	}
	if !missingRequired {
		out = append(out, Result{Name: "hooks", Status: OK, Detail: "all required hooks are installed"})
	}

	// Do the commands actually run?
	seen := map[string]bool{}
	for _, f := range all {
		bin, _, ok := f.BeaconWith(ours)
		if !ok || seen[bin] {
			continue
		}
		seen[bin] = true
		out = append(out, checkBinary(e, bin)...)
	}
	return out
}

func matcherOK(fs []claude.Found, want string) bool {
	for _, f := range fs {
		if f.Matcher == want {
			return true
		}
	}
	return false
}

func disabled(data []byte) bool {
	var d struct {
		Disable bool `json:"disableAllHooks"`
	}
	return jsonUnmarshal(data, &d) == nil && d.Disable
}

func checkBinary(e Env, bin string) []Result {
	var out []Result
	stat, look := e.Stat, e.LookPath
	if stat == nil {
		stat = os.Stat
	}
	if strings.ContainsRune(bin, '\\') {
		out = append(out, Result{Name: "hooks", Status: Warn, Detail: "the hook command uses backslashes in " + bin,
			Hints: []string{"bash treats backslashes as escapes; regenerate with: beacon hooks print --windows (it uses forward slashes)"}})
	}
	resolved := bin
	if !strings.ContainsAny(bin, `/\`) {
		if look == nil {
			return out
		}
		p, err := look(bin)
		if err != nil {
			return append(out, Result{Name: "hooks", Status: Warn, Detail: fmt.Sprintf("the hooks call %q by name, but it isn't on PATH here", bin),
				Hints: []string{"Claude Code's shell may have a different PATH; use the full path: beacon hooks print"}})
		}
		resolved = p
	} else if fi, err := stat(bin); err != nil || fi.IsDir() {
		return append(out, Result{Name: "hooks", Status: Fail, Detail: "the hook binary doesn't exist: " + bin,
			Hints: []string{"it moved or was reinstalled; regenerate the hooks with: beacon hooks print"}})
	}
	if e.Exe != "" && !sameFile(resolved, e.Exe) {
		out = append(out, Result{Name: "hooks", Status: Warn, Detail: fmt.Sprintf("the hooks run %s but you're running %s", resolved, e.Exe),
			Hints: []string{"if you reinstalled beacon somewhere else, regenerate the hooks: beacon hooks print"}})
	}
	return out
}

func sameFile(a, b string) bool {
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
	}
	return norm(a) == norm(b)
}

// ---- engine ----

func checkEngine(e Env) Result {
	if e.Store == nil {
		return Result{Name: "engine", Status: Info, Detail: "not checked"}
	}
	if e.Store.EngineRunning() {
		return Result{Name: "engine", Status: OK, Detail: "running (pid " + e.Store.EnginePID() + ")"}
	}
	return Result{Name: "engine", Status: Warn, Detail: "no daemon or TUI is running",
		Hints: []string{
			"hooks still work, but each one then updates the light itself, which over the cloud API takes about a second and can't meet the 150 ms target",
			"run `beacon daemon` (or leave the TUI open); `beacon autostart install` starts it at login",
		}}
}
