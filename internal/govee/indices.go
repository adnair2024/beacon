package govee

import (
	"sort"
	"strconv"
	"strings"
)

// FormatIndices renders segment indices compactly: [0 1 2 3 7] -> "0-3,7".
func FormatIndices(idx []int) string {
	s := append([]int(nil), idx...)
	sort.Ints(s)
	var parts []string
	for i := 0; i < len(s); {
		j := i
		for j+1 < len(s) && s[j+1] == s[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, strconv.Itoa(s[i])+"-"+strconv.Itoa(s[j]))
		} else {
			parts = append(parts, strconv.Itoa(s[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}
