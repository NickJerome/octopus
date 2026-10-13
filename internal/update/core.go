package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/utils/shutdown"
	"github.com/charmbracelet/log"
	"golang.org/x/mod/semver"
)

var updating atomic.Bool
var ErrUpdateInProgress = errors.New("an update is already being installed; wait for restart")

type Installation struct {
	Version    string `json:"version"`
	executable string
}

// UpdateCore verifies and installs one release. The caller must flush its response
// before calling Restart, which closes the HTTP server and the database.
func UpdateCore(ctx context.Context, version string) (*Installation, error) {
	if !updating.CompareAndSwap(false, true) {
		return nil, ErrUpdateInProgress
	}
	installed := false
	defer func() {
		if !installed {
			updating.Store(false)
		}
	}()
	status := GetStatus()
	if !status.Enabled {
		return nil, fmt.Errorf("self-update unavailable: %s", status.DisabledReason)
	}
	if !releaseVersionPattern.MatchString(version) || semver.Compare(version, conf.Version) <= 0 {
		return nil, fmt.Errorf("target must be a stable version newer than %s", conf.Version)
	}
	filename, err := getDownloadFilename()
	if err != nil {
		return nil, err
	}
	release, err := fetchRelease(ctx, status.Repository, version)
	if err != nil {
		return nil, err
	}
	checksums, err := downloadAsset(ctx, status.Repository, release, "SHA256SUMS", 1<<20)
	if err != nil {
		return nil, err
	}
	data, err := downloadAsset(ctx, status.Repository, release, filename, 256<<20)
	if err != nil {
		return nil, err
	}
	if err := verifyChecksum(data, checksums, filename); err != nil {
		return nil, err
	}
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "octopus-update-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	if err := unzip(data, tmpDir); err != nil {
		return nil, err
	}
	// Archive names do not depend on whether an operator renamed the installed binary.
	execName := "octopus"
	if runtime.GOOS == "windows" {
		execName += ".exe"
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := replaceExecutable(execPath, filepath.Join(tmpDir, execName)); err != nil {
		return nil, err
	}
	installed = true // Keep the guard until restart; no concurrent replacement is safe.
	log.Infof("installed %s from %s; previous binary retained as %s.old", version, status.Repository, execPath)
	return &Installation{Version: version, executable: execPath}, nil
}

func replaceExecutable(execPath, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("release executable missing: %w", err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("release executable must be a nonempty regular file")
	}
	current, err := os.Stat(execPath)
	if err != nil {
		return err
	}
	// Stage beside the executable so the final rename stays on the same filesystem.
	out, err := os.CreateTemp(filepath.Dir(execPath), ".octopus-update-*")
	if err != nil {
		return err
	}
	staged := out.Name()
	defer os.Remove(staged)
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Chmod(current.Mode().Perm()); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	oldPath := execPath + ".old"
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove previous backup: %w", err)
	}
	if err := os.Rename(execPath, oldPath); err != nil {
		return fmt.Errorf("back up executable: %w", err)
	}
	if err := os.Rename(staged, execPath); err != nil {
		if restoreErr := os.Rename(oldPath, execPath); restoreErr != nil {
			return fmt.Errorf("install failed: %v; restore failed: %w; recover manually from %s", err, restoreErr, oldPath)
		}
		return fmt.Errorf("install failed; previous executable restored: %w", err)
	}
	return nil
}

// getDownloadFilename 返回当前平台对应的发布归档名称。
func getDownloadFilename() (string, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	switch goos {
	case "windows":
		switch arch {
		case "amd64":
			return "octopus-windows-amd64.zip", nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "octopus-darwin-amd64.zip", nil
		case "arm64":
			return "octopus-darwin-arm64.zip", nil
		}
	case "linux":
		switch arch {
		case "386":
			return "octopus-linux-386.zip", nil
		case "amd64":
			return "octopus-linux-amd64.zip", nil
		case "arm":
			return "octopus-linux-arm.zip", nil
		case "arm64":
			return "octopus-linux-arm64.zip", nil
		}
	case "android":
		switch arch {
		case "386":
			return "octopus-android-386.zip", nil
		case "amd64":
			return "octopus-android-amd64.zip", nil
		case "arm":
			return "octopus-android-arm.zip", nil
		case "arm64":
			return "octopus-android-arm64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func (installation *Installation) Restart() {
	execPath := installation.executable
	shutdown.Shutdown()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
		os.Exit(1)
	}
}
