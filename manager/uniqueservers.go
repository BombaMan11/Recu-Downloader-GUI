package manager

import (
	"context"
	"fmt"
	"sort"
	"time"

	"Recurbate/engine"
	"Recurbate/tools"
)

// uniqueTries is how often a repeating row reads its page for another server.
const uniqueTries = 10

// Time Between Tries (Settings → Experimental, 1 s by default) comes before
// every one of those reads: asked again at once, the site hands out the same
// server (the old Unique Server Find waited 1 s too). uniqueSecond is how long
// one second of that setting lasts (the tests shorten it).
var uniqueSecond = time.Second

func (m *Manager) uniqueWaitLocked() time.Duration {
	return time.Duration(m.settings.UniqueWait * float64(uniqueSecond))
}

// readUnique (with Unique Servers ticked) reads a row's page for a new playlist (added, or started with
// Start, Start all or Force start) and, while its server is the one of the row
// it is compared with (see aboveLocked), reads it again: up to uniqueTries more
// times, Time Between Tries apart. The rows above are read first (one page at a time,
// top to bottom), so the server it is compared with is already final. A
// refusal on one of the extra reads keeps the playlist read before it.
func (m *Manager) readUnique(ctx context.Context, it *Item, env *engine.Env) (*engine.Resolved, error) {
	res, err := m.readPage(ctx, it, env)
	m.mu.Lock()
	on := m.settings.UniqueServers // Settings → Experimental
	wait := m.uniqueWaitLocked()
	m.mu.Unlock()
	for try := 0; on && err == nil && try < uniqueTries; try++ {
		m.mu.Lock()
		above := m.aboveLocked(it)
		m.mu.Unlock()
		if above == "" || res.Server != above {
			break
		}
		if !tools.Sleep(ctx, wait) {
			return nil, ctx.Err()
		}
		r, rerr := m.readPage(ctx, it, env)
		if rerr != nil {
			if isCancel(rerr) {
				return nil, rerr
			}
			break
		}
		res = r
	}
	return res, err
}

// ResetRepeatingServers is the Unique Servers button: one pass over the list
// from the top. A row on the same server as the row above it that it is
// compared with (see aboveLocked) reads its page again (up to uniqueTries
// times, Time Between Tries apart) until it is given another server. Rows that are
// downloading or being read are left alone. It reports false when a pass is
// already running.
func (m *Manager) ResetRepeatingServers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uniquePass {
		return false
	}
	var ids []int64
	for _, it := range m.uniqueOrderLocked() {
		ids = append(ids, it.ID)
	}
	m.uniquePass = true
	m.wg.Add(1)
	go m.uniquePassRun(ids)
	return true
}

func (m *Manager) uniquePassRun(ids []int64) {
	defer m.wg.Done()
	changed, same := 0, 0
	for _, id := range ids {
		m.mu.Lock()
		it, above := m.repeatingLocked(id)
		if it == nil || m.shutdown {
			m.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(m.rootCtx)
		it.cancel, it.busy, it.preResolving = cancel, true, true
		wait := m.uniqueWaitLocked()
		m.changedLocked(false)
		m.mu.Unlock()

		env := m.envFor(ctx, it, false)
		var res *engine.Resolved
		var err error
		for try := 0; try < uniqueTries; try++ {
			if !tools.Sleep(ctx, wait) {
				err = ctx.Err()
				break
			}
			if res, err = m.readPage(ctx, it, env); err != nil || res.Server != above {
				break
			}
		}
		cancel()

		m.mu.Lock()
		it.busy, it.preResolving, it.cancel = false, false, nil
		stop := false
		switch {
		case it.recat != nil && !it.removed:
			m.applyRecategoryLocked(it, *it.recat)
		case it.removed:
			m.cleanupRemovedLocked(it)
		case err == nil:
			it.res = res
			setServer(it, res.Server)
			if res.Server != above {
				changed++
			} else {
				same++
			}
		case !isCancel(err): // the site refused: asking more would only get more refusals
			m.notifyLocked("error", fmt.Sprintf("Unique servers stopped at %s: %v", displayName(it), userError(err)))
			stop = true
		}
		m.changedLocked(true)
		m.kick()
		m.mu.Unlock()
		if stop {
			break
		}
	}
	m.mu.Lock()
	m.uniquePass = false
	switch {
	case changed+same == 0:
		m.notifyLocked("info", "Unique servers: no repeating servers found.")
	default:
		m.notifyLocked("info", fmt.Sprintf("Unique servers: %d got another server, %d still repeat.", changed, same))
	}
	m.mu.Unlock()
}

// repeatingLocked returns the row with this id when it is on the same server
// as the row above it that it is compared with, and that server.
func (m *Manager) repeatingLocked(id int64) (*Item, string) {
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		above := m.aboveLocked(it)
		if above == "" || it.Server != above || it.busy || it.Status == StatusComplete ||
			it.Status == StatusResolving || it.Status == StatusDownloading {
			return nil, ""
		}
		return it, above
	}
	return nil, ""
}

// aboveLocked is the server of the nearest unfinished row above me that has
// one ("" for none). With Group Split Videos on, a part is compared only with
// the parts of its own group (name order) and a single row only with single rows.
func (m *Manager) aboveLocked(me *Item) string {
	order := m.uniqueOrderLocked()
	parts := map[string]int{} // with Group Split Videos on: rows per link
	if m.settings.GroupSplit {
		for _, o := range order {
			parts[o.URL]++
		}
	}
	above := ""
	for _, it := range order {
		if it == me {
			return above
		}
		grouped := parts[me.URL] > 1 || parts[it.URL] > 1
		if it.Status != StatusComplete && it.Server != "" && it.Server != "unknown" && (!grouped || it.URL == me.URL) {
			above = it.Server
		}
	}
	return ""
}

// uniqueOrderLocked is the list in queue order; with Group Split Videos on, the
// parts of each group take their group's places in name order, as the window
// shows them.
func (m *Manager) uniqueOrderLocked() []*Item {
	out := append([]*Item(nil), m.items...)
	if !m.settings.GroupSplit {
		return out
	}
	slots := map[string][]int{}
	for i, it := range out {
		slots[it.URL] = append(slots[it.URL], i)
	}
	for _, idx := range slots {
		parts := make([]*Item, len(idx))
		for k, i := range idx {
			parts[k] = out[i]
		}
		sort.SliceStable(parts, func(a, b int) bool { return displayName(parts[a]) < displayName(parts[b]) })
		for k, i := range idx {
			out[i] = parts[k]
		}
	}
	return out
}
