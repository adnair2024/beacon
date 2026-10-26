package autostart

import (
	"strings"
	"testing"
)

func TestSystemdQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/beacon":     "/usr/bin/beacon",
		"/opt/my apps/beacon": `"/opt/my apps/beacon"`,
		`/odd"path/beacon`:    `"/odd\"path/beacon"`,
		"/100%/beacon":        "/100%%/beacon",
		"/home/$USER/beacon":  "/home/$$USER/beacon",
		"":                    `""`,
	} {
		if got := systemdQuote(in); got != want {
			t.Errorf("%q -> %s, want %s", in, got, want)
		}
	}
}

func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit(Options{Exe: "/usr/local/bin/beacon"})
	for _, want := range []string{
		"[Unit]", "After=network-online.target", "[Service]", "ExecStart=/usr/local/bin/beacon daemon\n",
		"Restart=on-failure", "[Install]", "WantedBy=default.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q in:\n%s", want, u)
		}
	}
	if strings.Contains(u, "WorkingDirectory") || strings.Contains(u, "Environment=") {
		t.Fatal("optional lines appear only when asked for")
	}
	u = SystemdUnit(Options{
		Exe: "/opt/my apps/beacon", WorkDir: "/home/me/code/beacon",
		Env: map[string]string{"BEACON_STATE_DIR": "/var/lib/b b", "BEACON_CONFIG": "/etc/beacon.toml"},
	})
	if !strings.Contains(u, `ExecStart="/opt/my apps/beacon" daemon`) || !strings.Contains(u, "WorkingDirectory=/home/me/code/beacon") {
		t.Fatal(u)
	}
	// Environment lines are sorted (stable output) and quoted as a unit.
	i, j := strings.Index(u, "Environment=BEACON_CONFIG"), strings.Index(u, `Environment="BEACON_STATE_DIR=/var/lib/b b"`)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("%s", u)
	}
	// The key is never put in the unit file.
	if strings.Contains(strings.ToLower(u), "api_key") || strings.Contains(u, "GOVEE") {
		t.Fatal("a unit file is world-readable; it must not carry the API key")
	}
}

func TestTaskCommandAndArgs(t *testing.T) {
	o := Options{Exe: `C:\Users\Ash\Apps\it's here\beacon.exe`}
	cmd := TaskCommand(o)
	if !strings.Contains(cmd, `& 'C:\Users\Ash\Apps\it''s here\beacon.exe' daemon`) || !strings.Contains(cmd, "-WindowStyle Hidden") {
		t.Fatal(cmd)
	}
	args := SchtasksCreateArgs(o)
	want := []string{"/Create", "/TN", "beacon", "/SC", "ONLOGON", "/TR", cmd, "/RL", "LIMITED", "/F"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("%q", args)
	}
	for _, a := range args {
		if strings.Contains(a, "GOVEE") {
			t.Fatal("the key never goes in a scheduled task's command line")
		}
	}
}
