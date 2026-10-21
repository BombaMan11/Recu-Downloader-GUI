// Command uidemo runs the whole program against the mock site, so the user
// interface can be tried (and tested in a browser) without the real site:
//
//	go run ./internal/uidemo -dir /tmp/demo -port 47000
//
// It prints the address to open and the mock video links to paste.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"Recurbate/appdata"
	"Recurbate/internal/mocksite"
	"Recurbate/manager"
	"Recurbate/server"
)

func main() {
	dir := flag.String("dir", "", "working folder (required)")
	port := flag.Int("port", 0, "port (default: any)")
	lines := flag.Int("lines", 62, "segments per video (the first and last are dropped)")
	delay := flag.Int("delay", 20, "milliseconds per segment")
	noCreds := flag.Bool("no-creds", false, "start without a cookie / user agent")
	defaults := flag.Bool("defaults", false, "keep the settings of a new install (otherwise rows start by themselves, unfinished ones resume and the listener starts off with archive and backups on, which most tests were written for)")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "-dir is required")
		os.Exit(2)
	}

	site := mocksite.New()
	defer site.Close()
	site.Lines = *lines
	site.Delay = time.Duration(*delay) * time.Millisecond
	site.AlsoAccept("session=fresh")
	site.AlsoAccept("sid=abc; token=xyz") // so a replaced cookie can be tried in the window

	if *defaults { // a first start, as main does it
		if err := appdata.EnsureDefault(*dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	mgr, err := manager.New(*dir, "demo")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !*noCreds || !*defaults {
		s := mgr.Settings()
		if !*noCreds {
			s.UserAgent, s.Cookie = site.UA, site.Cookie
		}
		if !*defaults {
			s.AutoStart, s.AutoResume = true, true
			// the listener as it was before the defaults changed: off, archive and backups on
			s.Listen.Enabled, s.Listen.AllowPrivate = false, false
			s.Listen.NoArchive, s.Listen.NoBackups = false, false
		}
		if err := mgr.SetSettings(s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	clipFile := filepath.Join(*dir, "clipboard.txt") // the UI test "copies" by writing this file
	mgr.SetClipboard(manager.Clipboard{
		Seq: func() uint32 {
			st, err := os.Stat(clipFile)
			if err != nil {
				return 0
			}
			return uint32(st.ModTime().UnixNano()/1e6) ^ uint32(st.Size())
		},
		Read: func() (string, error) {
			b, err := os.ReadFile(clipFile)
			return string(b), err
		},
		// the UI test marks a copy as private by creating this file
		Private: func() bool { _, err := os.Stat(clipFile + ".private"); return err == nil },
	})
	mgr.Run()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	host := ln.Addr().String()
	quit := make(chan struct{})
	var once sync.Once
	srv := server.New(server.Options{
		Manager: mgr, Token: "demo-token", Host: host,
		OnQuit:     func() { once.Do(func() { close(quit) }) },
		Stop:       mgr.Shutdown,
		OpenFolder: func(string) error { return nil },
		// the UI test puts the "clipboard" text in this file
		ReadClipboard: func() (string, error) {
			b, err := os.ReadFile(filepath.Join(*dir, "clipboard.txt"))
			if err != nil {
				return "", fmt.Errorf("there is no text on the clipboard")
			}
			return string(b), nil
		},
		// the UI test reads this file to see what a double-click opened
		RevealFile: func(path string) error {
			return os.WriteFile(filepath.Join(*dir, "revealed.txt"), []byte(path), 0600)
		},
	})
	go http.Serve(ln, srv.Handler())

	fmt.Printf("URL http://%s/?t=demo-token\n", host)
	for id := 1; id <= 8; id++ {
		fmt.Printf("VIDEO %d %s server=%s\n", id, site.VideoURL(id), site.ServerOf(id))
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-quit:
	}
	srv.Close()
	mgr.Shutdown()
}
