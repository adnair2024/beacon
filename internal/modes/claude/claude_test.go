package claude

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/state"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

const ttl = 30 * time.Minute

func rec(c *state.Claude, ev Event, sid string, at time.Time) {
	Record(c, ev, Input{SessionID: sid}, at, ttl)
}

func TestAggregationWaitingBeatsWorkingBeatsReady(t *testing.T) {
	var c state.Claude
	rec(&c, SessionStart, "a", t0)
	if s, _ := Summarize(c, t0, ttl); s != Ready {
		t.Fatal(s)
	}
	rec(&c, EvWorking, "b", t0)
	if s, _ := Summarize(c, t0, ttl); s != Working {
		t.Fatal(s)
	}
	rec(&c, EvWaiting, "a", t0)
	if s, n := Summarize(c, t0, ttl); s != Waiting || n != 2 {
		t.Fatal(s, n)
	}
	rec(&c, EvReady, "a", t0)
	rec(&c, SessionEnd, "b", t0)
	if s, n := Summarize(c, t0, ttl); s != Ready || n != 1 {
		t.Fatal(s, n)
	}
	rec(&c, SessionEnd, "a", t0)
	if s, n := Summarize(c, t0, ttl); s != "" || n != 0 {
		t.Fatal(s, n)
	}
}

func TestChangedOnlyMovesWhenAggregateChanges(t *testing.T) {
	var c state.Claude
	rec(&c, EvWorking, "a", t0)
	first := c.Changed
	rec(&c, EvWorking, "a", t0.Add(time.Minute)) // another prompt, same aggregate
	rec(&c, EvWorking, "b", t0.Add(2*time.Minute))
	if !c.Changed.Equal(first) || c.Status != Working {
		t.Fatal("aggregate didn't change, Changed shouldn't move", c.Changed)
	}
	rec(&c, EvReady, "a", t0.Add(3*time.Minute)) // b still working
	if !c.Changed.Equal(first) {
		t.Fatal("still working overall")
	}
	rec(&c, EvReady, "b", t0.Add(4*time.Minute))
	if !c.Changed.Equal(t0.Add(4*time.Minute)) || c.Status != Ready {
		t.Fatal(c.Changed, c.Status)
	}
}

func TestTTLPrunesCrashedSessions(t *testing.T) {
	var c state.Claude
	rec(&c, EvWorking, "crashed", t0)
	rec(&c, EvReady, "alive", t0.Add(25*time.Minute))
	later := t0.Add(31 * time.Minute)
	if s, n := Summarize(c, later, ttl); s != Ready || n != 1 {
		t.Fatalf("stale session must be ignored at read time: %s %d", s, n)
	}
	rec(&c, EvReady, "alive", later) // a writer prunes it for real
	if _, ok := c.Sessions["crashed"]; ok {
		t.Fatal("crashed session should be pruned by the next write")
	}
	if at, ok := NextExpiry(c, later, ttl); !ok || !at.Equal(later.Add(ttl)) {
		t.Fatal(at, ok)
	}
}

func TestPreToolUseStyleEventRefreshesTTL(t *testing.T) {
	var c state.Claude
	rec(&c, EvWorking, "long", t0)
	rec(&c, EvWorking, "long", t0.Add(25*time.Minute)) // PreToolUse re-assert
	if s, _ := Summarize(c, t0.Add(50*time.Minute), ttl); s != Working {
		t.Fatal("refreshed session should still be live at +50m")
	}
}

func TestCurrentDetectsStaleAggregate(t *testing.T) {
	var c state.Claude
	rec(&c, EvWorking, "a", t0)
	rec(&c, EvReady, "b", t0)
	if v := Current(c, t0, ttl); v.Status != Working || v.Changed.IsZero() {
		t.Fatal(v)
	}
	// "a" goes stale while "b" is fresh: aggregate silently changed, so there is
	// no trustworthy Changed time (and thus no ready_hold).
	rec(&c, EvReady, "b", t0.Add(29*time.Minute))
	if v := Current(c, t0.Add(31*time.Minute), ttl); v.Status != Ready || !v.Changed.IsZero() {
		t.Fatal(v)
	}
}

func TestReadInput(t *testing.T) {
	good := `{"session_id":"abc","hook_event_name":"Stop","cwd":"/work","transcript_path":"/secret","extra":{"x":1}}`
	if in := ReadInput(strings.NewReader(good)); in.SessionID != "abc" || in.Cwd != "/work" {
		t.Fatal(in)
	}
	for _, bad := range []string{"", "not json", `{"cwd":"/x"}`, `{"session_id":""}`, `[1]`} {
		if in := ReadInput(strings.NewReader(bad)); in.SessionID != ManualSession {
			t.Errorf("%q -> %v", bad, in)
		}
	}
	if in := ReadInput(nil); in.SessionID != ManualSession {
		t.Fatal(in)
	}
	huge := `{"session_id":"abc","pad":"` + strings.Repeat("x", MaxPayload) + `"}`
	if in := ReadInput(strings.NewReader(huge)); in.SessionID != ManualSession {
		t.Fatal("payload over the size limit must not be trusted")
	}
	long := `{"session_id":"` + strings.Repeat("a", 1000) + `"}`
	if in := ReadInput(strings.NewReader(long)); len(in.SessionID) != maxField {
		t.Fatal(len(in.SessionID))
	}
}

func TestParseEvent(t *testing.T) {
	for _, e := range []string{"session-start", "working", "waiting", "ready", "session-end"} {
		if _, ok := ParseEvent(e); !ok {
			t.Errorf("%s should parse", e)
		}
	}
	if _, ok := ParseEvent("explode"); ok {
		t.Fatal("unknown event")
	}
}

// 20 hook processes at once, through the same locked code path the CLI uses.
func TestConcurrentHooksLoseNoSessions(t *testing.T) {
	store := state.Open(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := store.Update(func(f *state.File) error {
				Record(&f.Claude, EvWorking, Input{SessionID: fmt.Sprint("s", i)}, time.Now(), ttl)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, n := Summarize(f.Claude, time.Now(), ttl); n != 20 || f.Claude.Status != Working {
		t.Fatalf("lost updates: %d sessions, status %q", n, f.Claude.Status)
	}
}
