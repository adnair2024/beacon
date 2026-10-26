package schedule

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/fsutil"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidName reports whether name is safe to use as a file name. Profile names
// come from config and must never be able to reach outside the schedules dir.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Path is where a profile lives: <dir>/<name>.json.
func Path(dir, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("schedule: invalid profile name %q (use letters, digits, - and _)", name)
	}
	return filepath.Join(dir, name+".json"), nil
}

// lineCol turns a byte offset into a 1-based line and column.
func lineCol(b []byte, off int64) (line, col int) {
	off = min(off, int64(len(b)))
	line = 1 + bytes.Count(b[:off], []byte("\n"))
	col = int(off) - (bytes.LastIndexByte(b[:off], '\n') + 1) + 1
	return
}

// Parse decodes a profile and reports syntax errors with line and column.
// Unknown keys are ignored (forward compatibility).
func Parse(b []byte, source string) (Profile, error) {
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		var se *json.SyntaxError
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &se):
			l, c := lineCol(b, se.Offset)
			return p, fmt.Errorf("%s:%d:%d: %v", source, l, c, err)
		case errors.As(err, &te):
			l, c := lineCol(b, te.Offset)
			return p, fmt.Errorf("%s:%d:%d: %s should be %s, not %s", source, l, c, te.Field, te.Type, te.Value)
		}
		return p, fmt.Errorf("%s: %w", source, err)
	}
	if p.Version == 0 {
		p.Version = Version
	}
	return p, nil
}

// Load reads a profile. A missing file is not an error: the built-in default
// is returned with exists=false, so the shipped workday schedule works with
// nothing on disk.
func Load(dir, name string) (p Profile, exists bool, err error) {
	path, err := Path(dir, name)
	if err != nil {
		return Profile{}, false, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		d := Default()
		d.Name = name
		return d, false, nil
	}
	if err != nil {
		return Profile{}, false, fmt.Errorf("schedule: %w", err)
	}
	p, err = Parse(b, path)
	return p, err == nil, err
}

// Save writes a profile atomically (temp file + rename) as indented JSON, and
// keeps the previous version as <name>.json.bak. The file is machine-written,
// so there are no comments to lose.
func Save(dir, name string, p Profile, pal color.Palette) error {
	path, err := Path(dir, name)
	if err != nil {
		return err
	}
	if errs := p.Validate(pal); len(errs) > 0 {
		return fmt.Errorf("schedule: refusing to save: %w", errs[0])
	}
	p.Version, p.Name = Version, name
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("schedule: encode: %w", err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("schedule: %w", err)
	}
	if old, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
			return fmt.Errorf("schedule: backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".schedule-*.tmp")
	if err != nil {
		return fmt.Errorf("schedule: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("schedule: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("schedule: write: %w", err)
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	if err := fsutil.Replace(tmp.Name(), path); err != nil {
		return fmt.Errorf("schedule: replace: %w", err)
	}
	return nil
}
