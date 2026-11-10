package manager

import (
	"sort"
	"strings"
)

// named: the row has its real name (its page has been read), not just its link.
func named(it *Item) bool {
	return it.FileBase != "" || (it.res != nil && it.res.Playlist.Filename != "")
}

// sortQueueLocked puts the rows that are not finished in name order when Force
// Name Sort is on. Finished rows keep their places; rows that still show their
// link go last, in the order they were added.
func (m *Manager) sortQueueLocked() {
	dir := m.settings.ForceNameSort
	if dir == "" {
		return
	}
	var slots []int
	var rows []*Item
	for i, it := range m.items {
		if it.Status != StatusComplete {
			slots = append(slots, i)
			rows = append(rows, it)
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		na, nb := named(rows[a]), named(rows[b])
		if na != nb || !na {
			return na
		}
		c := compareNames(displayName(rows[a]), displayName(rows[b]))
		if dir == "za" {
			return c > 0
		}
		return c < 0
	})
	changed := false
	for k, i := range slots {
		if m.items[i] != rows[k] {
			m.items[i] = rows[k]
			changed = true
		}
	}
	if changed {
		m.changedLocked(true)
	}
}

// compareNames orders names the way a person reads them: capitals do not
// matter and numbers count by value (user9 before user10).
func compareNames(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			na, ra := digitRun(a)
			nb, rb := digitRun(b)
			na, nb = strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = ra, rb
			continue
		}
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		a, b = a[1:], b[1:]
	}
	return len(a) - len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func digitRun(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}
