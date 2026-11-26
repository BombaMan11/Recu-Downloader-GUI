// Package engine is the original download logic (page -> token -> API ->
// m3u8 -> segment list -> muxed .ts file). It no longer talks to channels or a
// global abort flag: everything is cancelled through Env.Ctx and reported via
// the Log / Warn callbacks, so many downloads can run and be stopped
// independently.
package engine

import (
	"context"
	"net/url"
)

// Env carries everything one download needs.
type Env struct {
	Ctx    context.Context
	Header map[string]string // Cookie, User-Agent, ... as configured
	MaxRes int               // maximum video height, 0 = no limit
	Log    func(string)      // stage text ("Downloading HTML", ...)
	Warn   func(string)      // non-fatal problems (retrying, ...)
}

func (e *Env) ctx() context.Context {
	if e.Ctx == nil {
		return context.Background()
	}
	return e.Ctx
}
func (e *Env) log(s string) {
	if e.Log != nil {
		e.Log(s)
	}
}
func (e *Env) warn(s string) {
	if e.Warn != nil {
		e.Warn(s)
	}
}
func (e *Env) maxRes() int {
	if e.MaxRes <= 0 {
		return 6969
	}
	return e.MaxRes
}

// originOf returns "scheme://host" of a page url.
func originOf(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
