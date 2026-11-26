package manager

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// nameRowsLocked gives the rows their file names as soon as the name of their
// link is known (one row of a link read is enough: its parts share the name).
// The parts of a link that are named together are numbered by start time: the
// one nearest 00:00:00 gets the plain name, the next name_01, and so on, so a
// single part has no number. A part added after its link was named gets the
// number after the highest one. A name already used by a file or another row
// is skipped for the next free number.
func (m *Manager) nameRowsLocked() {
	links := map[string][]*Item{}
	for _, it := range m.items {
		links[it.URL] = append(links[it.URL], it)
	}
	for _, rows := range links {
		name := ""
		var fresh []*Item
		for _, it := range rows {
			if it.res != nil && it.res.Playlist.Filename != "" {
				name = it.res.Playlist.Filename
			}
			if it.FileBase == "" && it.Status != StatusComplete {
				fresh = append(fresh, it)
			}
		}
		if name == "" || len(fresh) == 0 {
			continue
		}
		n := 0
		for _, it := range rows {
			if k, ok := partNumber(it.FileBase, name); ok && k >= n {
				n = k + 1
			}
		}
		sort.SliceStable(fresh, func(a, b int) bool {
			sa, ea, _ := sectionSeconds(fresh[a])
			sb, eb, _ := sectionSeconds(fresh[b])
			return sa < sb || sa == sb && ea < eb
		})
		for _, it := range fresh {
			for m.nameTakenLocked(numberedName(name, n), it) {
				n++
			}
			it.FileBase = numberedName(name, n)
			n++
		}
		m.changedLocked(true)
	}
}

// partNumber: the number base has as a part of name ("name" 0, "name_07" 7).
func partNumber(base, name string) (int, bool) {
	if base == name {
		return 0, true
	}
	k, err := strconv.Atoi(strings.TrimPrefix(base, name+"_"))
	return k, err == nil && strings.HasPrefix(base, name+"_") && k > 0
}

// nameTakenLocked: a file of that name is in the row's folders, or another row has it.
func (m *Manager) nameTakenLocked(base string, self *Item) bool {
	if exists(filepath.Join(m.incDirLocked(self), base+".ts")) || exists(filepath.Join(m.doneDirLocked(self), base+".ts")) {
		return true
	}
	for _, o := range m.items {
		if o != self && o.FileBase == base {
			return true
		}
	}
	return false
}
