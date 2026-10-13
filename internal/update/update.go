package update

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

type httpError struct{ status int }

func (e *httpError) Error() string { return fmt.Sprintf("GitHub request returned HTTP %d", e.status) }

// doRequestWithFallback performs an HTTP GET request, first without proxy, then with proxy if failed.
func doRequestWithFallback(ctx context.Context, url, accept string, limit int64) ([]byte, error) {
	data, err := doRequest(ctx, url, accept, limit, false)
	if err == nil {
		return data, nil
	}
	var responseErr *httpError
	if ctx.Err() != nil || (errors.As(err, &responseErr) && responseErr.status >= 400 && responseErr.status < 500) {
		return nil, err
	}
	log.Warnf("direct request failed, trying with proxy: %v", err)
	data, proxyErr := doRequest(ctx, url, accept, limit, true)
	if proxyErr != nil {
		return nil, fmt.Errorf("direct request failed (%v); proxy request failed: %w", err, proxyErr)
	}
	return data, nil
}

func doRequest(parent context.Context, url, accept string, limit int64, useProxy bool) ([]byte, error) {
	timeout := 30 * time.Second
	if accept == "application/octet-stream" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	var hc *http.Client
	var err error
	if useProxy {
		hc, err = rhttp.Proxy()
	} else {
		hc, err = rhttp.Direct()
	}
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Debugf("new request failed: %v", err)
		return nil, err
	}

	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := hc.Do(req)
	if err != nil {
		log.Debugf("request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpError{status: resp.StatusCode}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		log.Debugf("read body failed: %v", err)
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GitHub response exceeds %d bytes", limit)
	}
	return data, nil
}

func unzip(data []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Debugf("new zip reader failed: %v", err)
		return err
	}

	var expanded uint64
	for _, f := range r.File {
		if f.UncompressedSize64 > (512<<20)-expanded {
			return fmt.Errorf("expanded release exceeds 512 MiB")
		}
		expanded += f.UncompressedSize64
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path: %s", fpath)
		}

		info := f.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(fpath, os.ModePerm); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release archive must not contain symbolic links")
		}

		if err := extractFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, fpath string) error {
	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return err
	}

	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm())
	if err != nil {
		return err
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return err
	}
	defer rc.Close()

	written, err := io.Copy(outFile, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
	if err != nil {
		log.Debugf("copy failed: %v", err)
		return err
	}
	if written > int64(f.UncompressedSize64) {
		return fmt.Errorf("release file exceeds declared size")
	}
	return outFile.Close()
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
