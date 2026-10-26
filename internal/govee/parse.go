package govee

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/adnair2024/beacon/internal/color"
)

// ParseState turns what a person types ("orange", "#FF8C00", "3000k", "on",
// "off") into a State. It's shared by `beacon set` and the TUI override picker.
// brightness is 1-100; the device clamps it to its own range later.
func ParseState(arg string, brightness int, pal color.Palette) (State, error) {
	s := State{On: true, Brightness: brightness}
	switch a := strings.ToLower(strings.TrimSpace(arg)); {
	case a == "off":
		return State{}, nil
	case a == "on":
		return s, nil
	case strings.HasSuffix(a, "k"):
		if k, err := strconv.Atoi(strings.TrimSuffix(a, "k")); err == nil {
			s.Kelvin = k
			return s, nil
		}
	}
	c, err := color.Parse(arg, pal)
	if err != nil {
		return s, fmt.Errorf("%w (use a color, a kelvin like 3000k, on, or off)", err)
	}
	s.Color = &c
	return s, nil
}

// splitTop splits s on sep, ignoring separators inside parentheses so that
// "rgb(1,2,3)" survives a comma-separated list.
func splitTop(s string, sep rune) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')':
			depth = max(0, depth-1)
		case r == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + len(string(r))
		}
	}
	return append(parts, s[start:])
}

// ParseSegments reads a spec like "0-4=#FF8C00,5-9=teal@30": comma-separated
// "range=color" items, where range is N or A-B and an optional @N sets that
// item's brightness (1-100). Later items win for an index named twice. Indices
// are not checked against a device here (see Capabilities.CheckSegments).
func ParseSegments(spec string, pal color.Palette) ([]Segment, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("empty segment spec (example: 0-4=orange,5-9=#FF4FA3@30)")
	}
	byIdx := map[int]Segment{}
	for _, item := range splitTop(spec, ',') {
		item = strings.TrimSpace(item)
		rng, val, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("segment item %q: want range=color (example: 0-4=orange)", item)
		}
		lo, hi, err := parseRange(strings.TrimSpace(rng))
		if err != nil {
			return nil, fmt.Errorf("segment item %q: %w", item, err)
		}
		bri := 0
		if v, b, has := strings.Cut(val, "@"); has {
			n, err := strconv.Atoi(strings.TrimSpace(b))
			if err != nil || n < 1 || n > 100 {
				return nil, fmt.Errorf("segment item %q: brightness after @ must be 1-100", item)
			}
			val, bri = v, n
		}
		c, err := color.Parse(strings.TrimSpace(val), pal)
		if err != nil {
			return nil, fmt.Errorf("segment item %q: %w", item, err)
		}
		for i := lo; i <= hi; i++ {
			byIdx[i] = Segment{Index: i, Color: c, Brightness: bri}
		}
	}
	out := make([]Segment, 0, len(byIdx))
	for _, s := range byIdx {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

func parseRange(s string) (lo, hi int, err error) {
	a, b, isRange := strings.Cut(s, "-")
	if lo, err = strconv.Atoi(strings.TrimSpace(a)); err != nil || lo < 0 {
		return 0, 0, fmt.Errorf("%q is not a segment number or range like 0-4", s)
	}
	if !isRange {
		return lo, lo, nil
	}
	if hi, err = strconv.Atoi(strings.TrimSpace(b)); err != nil || hi < lo {
		return 0, 0, fmt.Errorf("%q is not a valid range (low-high)", s)
	}
	return lo, hi, nil
}
