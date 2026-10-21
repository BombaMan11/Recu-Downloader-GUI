package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"Recurbate/appdata"
	"Recurbate/applog"
	"Recurbate/launch"
	"Recurbate/manager"
	"Recurbate/server"
)

// set at build time: -ldflags "-X main.tag=v1.2.3"
var tag = "dev"

type instanceInfo struct {
	Host  string `json:"host"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func (i instanceInfo) windowURL() string { return "http://" + i.Host + "/?t=" + i.Token }

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// another copy of the program is already serving this folder: show its window
func existingInstance(path string) *instanceInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var info instanceInfo
	if json.Unmarshal(data, &info) != nil || info.Host == "" || info.Token == "" {
		return nil
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + info.Host + "/api/ping?t=" + info.Token)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	return &info
}

// profileDir is the window's browser profile, kept in the data folder with
// the settings, queue and log.
func profileDir(base string) string {
	return appdata.Load(base).ActiveProfileDir(base)
}

// removeOldProfile deletes the window profile an earlier version kept in the
// user's cache folder, so nothing is left outside the data folder.
func removeOldProfile() {
	cache, err := os.UserCacheDir()
	if err != nil {
		return
	}
	dir := filepath.Join(cache, "Recurbate")
	if os.RemoveAll(filepath.Join(dir, "window")) == nil {
		os.Remove(dir) // only succeeds when empty
	}
}

func main() {
	dirFlag := flag.String("dir", "", "folder for settings, the queue and the Done / Incomplete / M3U8 folders (default: next to the program)")
	port := flag.Int("port", 0, "port to listen on (default: any free port)")
	noWindow := flag.Bool("no-window", false, "do not open a window, just print the address")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *version {
		fmt.Println("Recurbate", tag)
		return
	}

	base := *dirFlag
	if base == "" {
		base = exeDir()
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		launch.ShowError("Recurbate", fmt.Sprintf("Can not use the folder %s:\n%v", base, err))
		os.Exit(1)
	}
	// the data folder (settings, queue, log, window data) can be changed in
	// Settings; it is remembered in a small file next to the program. A fresh
	// install uses a "Recu-Settings" folder there.
	if err := appdata.EnsureDefault(base); err != nil {
		log.Printf("can not remember the settings folder: %v", err)
	}
	if err := applog.Open(appdata.Load(base).DataDir(base)); err != nil {
		log.Printf("can not write the log file: %v", err)
	}
	log.SetFlags(log.LstdFlags)

	instPath := filepath.Join(base, "instance.json")
	if inst := existingInstance(instPath); inst != nil {
		log.Printf("already running (pid %d), showing its window", inst.PID)
		if !*noWindow && !launch.FocusWindow("Recurbate") {
			// no window to bring forward (it was closed or is in a browser tab)
			launch.OpenWindow(inst.windowURL(), profileDir(base))
		}
		return
	}

	mgr, err := manager.New(base, tag)
	if err != nil {
		log.Printf("start failed: %v", err)
		launch.ShowError("Recurbate", fmt.Sprintf("Could not start:\n%v\n\nFiles are in %s", err, base))
		os.Exit(1)
	}
	mgr.SetClipboard(manager.Clipboard{
		Seq:     launch.ClipboardSeq,
		Read:    launch.ReadClipboard,
		Private: launch.ClipboardIsPrivate, // private browsing windows, password managers
	})
	mgr.Run()
	removeOldProfile()
	if err := appdata.FinishProfileMove(base); err != nil {
		log.Printf("could not move the window data yet (will try again at the next start): %v", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Printf("listen failed: %v", err)
		launch.ShowError("Recurbate", fmt.Sprintf("Could not open a local port:\n%v", err))
		os.Exit(1)
	}
	host := ln.Addr().String()
	token := randomHex(16)
	url := fmt.Sprintf("http://%s/?t=%s", host, token)

	quit := make(chan struct{})
	var quitOnce sync.Once
	requestQuit := func() { quitOnce.Do(func() { close(quit) }) }
	srv := server.New(server.Options{
		Manager:       mgr,
		Token:         token,
		Host:          host,
		ExitOnClose:   !*noWindow,
		OnQuit:        requestQuit,
		Stop:          mgr.Shutdown,
		OpenFolder:    launch.OpenFolder,
		PickFolder:    launch.PickFolder,
		RevealFile:    launch.RevealFile,
		ReadClipboard: launch.ReadClipboard,
	})
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("server: %v", err)
			requestQuit()
		}
	}()
	if data, err := json.Marshal(instanceInfo{Host: host, Token: token, PID: os.Getpid()}); err == nil {
		os.WriteFile(instPath, data, 0600)
	}
	defer os.Remove(instPath)

	log.Printf("Recurbate %s listening on http://%s (folder %s)", tag, host, base)
	if *noWindow {
		fmt.Println(url)
	} else if err := launch.OpenWindow(url, profileDir(base)); err != nil {
		log.Printf("could not open a window: %v", err)
		launch.ShowError("Recurbate", "Could not open a window. Open this address in a browser:\n"+url)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-quit:
	}

	log.Printf("shutting down")
	srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	httpSrv.Shutdown(ctx)
	cancel()
	mgr.Shutdown()
}
