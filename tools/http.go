package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// One shared client so connections to the CDN are reused between segments.
// (The per-request deadline is applied through the context instead of
// http.Client.Timeout, so it still covers connecting, headers and the body.)
var httpClient = &http.Client{Transport: newTransport()}

func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 64
	t.MaxIdleConnsPerHost = 16
	t.IdleConnTimeout = 30 * time.Second
	return t
}

// Sleep waits for d or until ctx is cancelled. It reports whether the full
// duration elapsed.
func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Returns the raw data from the URL. ctx may be nil.
func Request(ctx context.Context, url string, timeout int, header map[string]string, body []byte, Type string) (data []byte, statusCode int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, Type, url, rd)
	if err != nil {
		err = fmt.Errorf("http.NewRequest:%w", err)
		return
	}
	for key, value := range header {
		req.Header.Set(key, value)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("client.Do:%w", err)
		return
	}
	statusCode = resp.StatusCode
	defer resp.Body.Close()
	data, err = io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("io.ReadAll:%w", err)
	}
	return
}

// RequestRetry repeats Request until one of successStatusCode is returned or
// the retries run out. onRetry (optional) is told about every unsuccessful
// HTTP status. It gives up immediately when ctx is cancelled.
func RequestRetry(ctx context.Context, url string, retry int, onRetry func(error),
	successStatusCode []int, timeoutFailIncreaseInterval int, failSleepTime time.Duration,
	timeout int, header map[string]string, body []byte, Type string) (data []byte, statusCode int, err error,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	retryCount := 0
	for {
		data, statusCode, err = Request(ctx, url, timeout, header, body, Type)
		if err == nil {
			for _, ok := range successStatusCode {
				if ok == statusCode {
					return
				}
			}
			err = fmt.Errorf("%s, status code: %d", ShortenString(string(data), 100), statusCode)
			if onRetry != nil {
				onRetry(err)
			}
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, 0, cerr
		}
		if retryCount > retry {
			return
		}
		retryCount++
		timeout += timeoutFailIncreaseInterval
		if !Sleep(ctx, failSleepTime) {
			return nil, 0, ctx.Err()
		}
	}
}
