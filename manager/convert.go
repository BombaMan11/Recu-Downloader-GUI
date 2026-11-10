package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"Recurbate/launch"
	"Recurbate/tools"
)

// queueConvertLocked lines a finished video up for conversion to mp4.
func (m *Manager) queueConvertLocked(it *Item) {
	if !m.settings.ConvertMP4 || !strings.HasSuffix(it.DonePath, ".ts") {
		return
	}
	it.convertNote = "Waiting to convert to MP4"
	m.convertQ = append(m.convertQ, it)
	select {
	case m.convertCh <- struct{}{}:
	default:
	}
}

// convertLoop converts the queued videos one at a time: several ffmpeg runs at
// once can bring a computer down.
func (m *Manager) convertLoop() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		var it *Item
		if len(m.convertQ) > 0 {
			it, m.convertQ = m.convertQ[0], m.convertQ[1:]
		}
		m.mu.Unlock()
		if it == nil {
			select {
			case <-m.convertCh:
				continue
			case <-m.rootCtx.Done():
				return
			}
		}
		m.convert(it)
	}
}

func (m *Manager) convert(it *Item) {
	m.mu.Lock()
	src := it.DonePath
	del := m.settings.DeleteAfterConvert
	ffDir := m.settings.FFmpegDir
	it.convertNote = "Converting to MP4"
	m.changedLocked(false)
	m.mu.Unlock()

	dst, err := convertToMP4(m.rootCtx, ffDir, src)

	m.mu.Lock()
	defer m.mu.Unlock()
	it.convertNote = ""
	m.changedLocked(true)
	switch {
	case err != nil:
		if m.rootCtx.Err() == nil {
			m.notifyLocked("error", fmt.Sprintf("Could not convert %s to MP4: %v", displayName(it), err))
		}
	case it.removed || it.DonePath != src: // the row went away meanwhile
		os.Remove(dst)
	default:
		it.DonePath = dst
		it.FileBase = strings.TrimSuffix(filepath.Base(dst), ".mp4")
		if st, err := os.Stat(dst); err == nil {
			it.FileSize, it.FullSize = st.Size(), st.Size()
		}
		if del {
			if err := os.Remove(src); err != nil {
				m.notifyLocked("warn", fmt.Sprintf("Converted %s, but could not delete the .ts: %v", displayName(it), err))
				return
			}
		}
		m.notifyLocked("info", "Converted to MP4: "+displayName(it))
	}
}

// convertToMP4 turns a .ts next to itself into an .mp4 and returns its path.
// A probe pass counts timestamp discontinuities; with any, the timestamps are
// fixed and the audio re-encoded, otherwise everything is copied.
func convertToMP4(ctx context.Context, ffDir, src string) (string, error) {
	ff, err := findFFmpeg(ffDir)
	if err != nil {
		return "", err
	}
	dst := uniquePath(filepath.Dir(src), strings.TrimSuffix(filepath.Base(src), ".ts"), ".mp4")
	part := dst + ".part" // never looks like a finished mp4
	probe, _ := runFFmpeg(ctx, ff, "-hide_banner", "-nostats", "-i", src, "-map", "0:v:0", "-map", "0:a:0?", "-c", "copy", "-f", "null", "-")
	args := []string{"-loglevel", "error", "-hide_banner", "-y"}
	if strings.Contains(probe, "timestamp discontinuity") {
		aac := "aac"
		if runtime.GOOS == "windows" {
			aac = "aac_mf"
		}
		args = append(args, "-copyts", "-start_at_zero", "-i", src, "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "copy",
			"-c:a", aac, "-aac_coder", "fast", "-b:a", "192k", "-af", "aresample=async=1000")
	} else {
		args = append(args, "-i", src, "-c:v", "copy", "-c:a", "copy")
	}
	args = append(args, "-movflags", "+faststart", "-f", "mp4", part)
	out, err := runFFmpeg(ctx, ff, args...)
	if err == nil {
		err = os.Rename(part, dst)
	}
	if err != nil {
		os.Remove(part)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if msg := strings.TrimSpace(out); msg != "" {
			lines := strings.Split(msg, "\n")
			err = errors.New(tools.ShortenString(strings.TrimSpace(lines[len(lines)-1]), 120))
		}
		return "", err
	}
	return dst, nil
}

// findFFmpeg is the ffmpeg in the chosen folder, or the one in PATH when the
// folder is blank.
func findFFmpeg(dir string) (string, error) {
	if dir == "" {
		p, err := exec.LookPath("ffmpeg")
		if err != nil {
			return "", errors.New("ffmpeg was not found in PATH. Choose its folder in Settings → Experimental → FFmpeg Location")
		}
		return p, nil
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", fmt.Errorf("%s was not found in %s", name, dir)
	}
	return p, nil
}

func runFFmpeg(ctx context.Context, ff string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, ff, args...)
	launch.HideWindow(cmd)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}
