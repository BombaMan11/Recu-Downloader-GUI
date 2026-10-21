package engine

import (
	"errors"
	"fmt"
	"os"
	"time"

	"Recurbate/playlist"
	"Recurbate/tools"
	"Recurbate/tools/avgBuffer"
)

// ErrExpired means the CDN answered 410: the segment links have expired and
// the video has to be resolved again before it can continue.
var ErrExpired = errors.New("download expired")

// MuxJob describes one (possibly resumed) download.
type MuxJob struct {
	Playlist   *playlist.Playlist
	Section    [2]float32 // start/end percent of the video
	Offset     int        // next segment (absolute) to fetch when Imported
	Imported   bool       // continuation of a previous run
	ResumeSize int64      // size the file had at Offset, <0 if unknown
	Path       string     // .ts file to write
	// Expires is when the segment links stop working (zero = unknown). The
	// download hands back ErrExpired a little before that so the playlist can
	// be read again while the old links still work, instead of running into
	// 403 / 410 answers.
	Expires time.Time
}

// ExpiryLead is how long before the links expire the playlist is refreshed.
var ExpiryLead = time.Minute

// Progress is reported after every segment.
type Progress struct {
	Next    int     // next segment index to download (absolute)
	Start   int     // first segment of the requested section
	End     int     // one past the last segment of the section
	Bytes   int64   // current size of the output file
	Percent float32 // of the whole section, also when this run resumed part-way
	Speed   float32 // bytes per second (moving average)
	EtaMin  float32 // minutes remaining
}

// Mux downloads the transport streams and saves them to a file
// (the original Mux()).
func Mux(env *Env, job MuxJob, onProgress func(Progress)) error {
	ctx := env.ctx()
	if err := ctx.Err(); err != nil {
		return err
	}
	pl := job.Playlist
	sec := job.Section
	if sec[0] > 100 || sec[1] <= sec[0] {
		return fmt.Errorf("duration format error")
	}
	if sec[0] < 0 {
		sec[0] = 0
	}
	if sec[1] > 100 {
		sec[1] = 100
	}
	n := pl.Len()
	start := int(float32(n) * sec[0] / 100)
	end := int(float32(n) * sec[1] / 100)
	section := start

	// checks if continuation of previous run
	var file *os.File
	var written int64
	imported := job.Imported
	if imported {
		st, serr := os.Stat(job.Path)
		switch {
		case serr != nil:
			env.warn("partial file not found, starting this video again from its beginning")
			imported = false
		case job.ResumeSize >= 0 && st.Size() < job.ResumeSize:
			env.warn("partial file is shorter than expected, starting this video again from its beginning")
			imported = false
		default:
			written = st.Size()
			if job.ResumeSize >= 0 && st.Size() > job.ResumeSize {
				// bytes after the last recorded segment belong to an interrupted write
				if err := os.Truncate(job.Path, job.ResumeSize); err != nil {
					return fmt.Errorf("can not trim partial file: %v", err)
				}
				written = job.ResumeSize
			}
			var err error
			file, err = os.OpenFile(job.Path, os.O_APPEND|os.O_WRONLY, 0666)
			if err != nil {
				env.warn(fmt.Sprintf("original file not found, creating new one: %v", err))
				imported = false
				file = nil
			}
		}
	}
	if imported {
		start = job.Offset
	} else {
		written = 0
		var err error
		file, err = os.OpenFile(job.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
		if err != nil {
			return fmt.Errorf("can not create file: %v", err)
		}
	}
	defer file.Close()
	if end > n {
		end = n
	}
	if start > end {
		start = end
	}
	if start < 0 {
		start = 0
	}

	// muxing loop
	total := end - start
	if total == 0 {
		if onProgress != nil {
			onProgress(Progress{Next: end, Start: section, End: end, Bytes: written, Percent: 100})
		}
		return nil
	}
	avgdur := avgBuffer.New(25)
	avgsize := avgBuffer.New(25)
	var data []byte
	for i := start; i < end; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !job.Expires.IsZero() && time.Now().Add(ExpiryLead).After(job.Expires) {
			env.warn("links are about to expire, refreshing playlist")
			return fmt.Errorf("%w (links about to expire)", ErrExpired)
		}
		startTime := time.Now()
		if err := muxDownloadLoop(env, &data, pl.List[i], 10, 5); err != nil {
			return err
		}
		endDur := float32(time.Since(startTime).Minutes())
		if _, err := file.Write(data); err != nil {
			return fmt.Errorf("can not write file: %v", err)
		}
		written += int64(len(data))
		// Calculate User Interface Timings
		avgsize.Add(float32(len(data)))
		avgdur.Add(endDur)
		getavgdur := avgdur.Average()
		p := Progress{
			Next:    i + 1,
			Start:   section,
			End:     end,
			Bytes:   written,
			Percent: float32(i+1-section) / float32(end-section) * 100,
		}
		if getavgdur > 0 {
			p.Speed = avgsize.Average() / (getavgdur * 60)
			p.EtaMin = getavgdur * float32(end-i-1)
		}
		if onProgress != nil {
			onProgress(p)
		}
	}
	return nil
}

// download retry loop for Mux()
//
// As in the original: 5 retries, 429 is asked again, 410 gives up at once.
// Added: a 403 / 401 gets one more try and then ErrDenied (expired signed
// link or a block: the caller reads the playlist again), and 429 backs off
// instead of asking every 100 ms for as long as it takes, which only gets the
// client blocked.
func muxDownloadLoop(env *Env, data *[]byte, url string, timeout, maxRetry int) (err error) {
	ctx := env.ctx()
	retry := 0
	limited := 0
	backoff := 100 * time.Millisecond
	for {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		var status int
		*data, status, err = tools.Request(ctx, url, timeout, env.Header, nil, "GET")
		if err == nil && status == 200 {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if status == 429 {
			limited++
			if limited > 40 {
				return fmt.Errorf("%w (%v)", ErrDenied, &StatusError{Code: status, Body: string(*data)})
			}
			tools.Sleep(ctx, backoff)
			if backoff < 10*time.Second {
				backoff *= 2
			}
			continue
		}
		denied := err == nil && (status == 403 || status == 401)
		if status == 410 {
			env.warn("download expired")
			retry = maxRetry
		}
		if denied && retry >= 1 {
			retry = maxRetry
		}
		retry++
		if err == nil {
			err = &StatusError{Code: status, Body: string(*data)}
		} else {
			timeout += 30
		}
		if retry > maxRetry {
			switch {
			case status == 410:
				return fmt.Errorf("%w (%v)", ErrExpired, err)
			case denied:
				return fmt.Errorf("%w (%v)", ErrDenied, err)
			}
			return
		}
		env.warn(fmt.Sprintf("%v, retrying...", tools.ShortenString(err, 60)))
		tools.Sleep(ctx, time.Second)
	}
}
