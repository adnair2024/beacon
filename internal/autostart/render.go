package autostart

import (
	"fmt"
	"sort"
	"strings"
)

// systemdQuote quotes one word for a unit file's ExecStart=/Environment=.
// systemd expands "%x" specifiers and "$VAR" in these, so a literal % or $ is
// doubled, and a word with spaces or quotes goes in double quotes.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "%", "%%"), "$", "$$")
	if s == "" || strings.ContainsAny(s, " \t\"'\\") {
		s = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return s
}

// SystemdUnit is the user service file.
func SystemdUnit(o Options) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=beacon: Govee desk status light (daemon)\n")
	b.WriteString("After=network-online.target\nWants=network-online.target\n\n")
	b.WriteString("[Service]\nType=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s daemon\n", systemdQuote(o.Exe))
	b.WriteString("Restart=on-failure\nRestartSec=5\n")
	if o.WorkDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", systemdQuote(o.WorkDir))
	}
	keys := make([]string, 0, len(o.Env))
	for k := range o.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+o.Env[k]))
	}
	b.WriteString("\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// TaskName is the Task Scheduler entry's name.
const TaskName = "beacon"

// psQuote single-quotes a word for PowerShell (a literal ' is doubled).
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// TaskCommand is the command line the scheduled task runs. PowerShell with a
// hidden window keeps a console from sitting open at every login; the daemon
// logs to its file, not the console.
func TaskCommand(o Options) string {
	return `powershell.exe -NoProfile -WindowStyle Hidden -Command "& ` + psQuote(o.Exe) + ` daemon"`
}

// SchtasksCreateArgs are the arguments to `schtasks` that create the task.
// Go quotes each argument for the Windows command line, so the embedded
// quotes in /TR arrive intact.
func SchtasksCreateArgs(o Options) []string {
	return []string{"/Create", "/TN", TaskName, "/SC", "ONLOGON", "/TR", TaskCommand(o), "/RL", "LIMITED", "/F"}
}
