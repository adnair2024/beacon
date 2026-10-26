package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Allowed are the only variables a .env file may set.
var Allowed = []string{"GOVEE_API_KEY", "BEACON_CONFIG", "BEACON_STATE_DIR"}

// bom is the UTF-8 byte order mark some editors put at the start of a file.
var bom = string(rune(0xFEFF))

var keyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse reads KEY=VALUE lines. It understands blank lines, # comments, an
// optional "export " prefix, single- and double-quoted values (double quotes
// honor \" \\ \n), and trailing " # comments" on unquoted values. Problems
// are collected per line so one typo doesn't hide the rest.
func Parse(r io.Reader) (map[string]string, []error) {
	vars := map[string]string{}
	var errs []error
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), bom)) // tolerate a BOM and CRLF
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !keyRE.MatchString(k) {
			errs = append(errs, fmt.Errorf("line %d: expected KEY=VALUE", n))
			continue
		}
		val, err := value(strings.TrimSpace(v))
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d: %s: %w", n, k, err))
			continue
		}
		vars[k] = val
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	return vars, errs
}

func value(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	switch q := v[0]; q {
	case '"', '\'':
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			c := v[i]
			switch {
			case c == q:
				if rest := strings.TrimSpace(v[i+1:]); rest != "" && !strings.HasPrefix(rest, "#") {
					return "", errors.New("unexpected text after the closing quote")
				}
				return b.String(), nil
			case c == '\\' && q == '"' && i+1 < len(v):
				i++
				switch v[i] {
				case 'n':
					b.WriteByte('\n')
				case '"', '\\':
					b.WriteByte(v[i])
				default:
					b.WriteByte('\\')
					b.WriteByte(v[i])
				}
			default:
				b.WriteByte(c)
			}
		}
		return "", errors.New("unterminated quote")
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v), nil
}

// Result says what Load did, for diagnostics. It never contains values.
type Result struct {
	Path    string
	Set     []string // variables taken from the file
	Skipped []string // variables ignored because they are not allowed
	Errors  []error  // parse problems
}

// Load reads path (a missing file is fine) and sets each allowed, non-empty
// variable that isn't already set in the real environment.
func Load(path string) Result {
	res := Result{Path: path}
	f, err := os.Open(path)
	if err != nil {
		return res
	}
	defer f.Close()
	vars, errs := Parse(f)
	res.Errors = errs
	for k, v := range vars {
		switch {
		case !allowed(k):
			res.Skipped = append(res.Skipped, k)
		case v == "":
			// An empty template line (GOVEE_API_KEY=) must not count as a value.
		case os.Getenv(k) != "":
			// The real environment wins.
		default:
			if err := os.Setenv(k, v); err == nil {
				res.Set = append(res.Set, k)
			}
		}
	}
	return res
}

func allowed(k string) bool {
	for _, a := range Allowed {
		if a == k {
			return true
		}
	}
	return false
}
