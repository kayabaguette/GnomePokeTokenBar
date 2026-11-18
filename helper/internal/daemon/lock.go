package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Dirs returns the state and cache directories (protocol §2.1): $XDG_STATE_HOME/poketokenbar and
// $XDG_CACHE_HOME/poketokenbar, with the XDG defaults. PTB_STATE_DIR overrides both: the state lives
// in it and the cache in its "cache" subdirectory.
func Dirs(getenv func(string) string) (state, cache string, err error) {
	if dir := getenv("PTB_STATE_DIR"); dir != "" {
		return dir, filepath.Join(dir, "cache"), nil
	}
	home := getenv("HOME")
	base := func(env, fallback string) (string, error) {
		if v := getenv(env); v != "" && filepath.IsAbs(v) {
			return v, nil
		}
		if home == "" {
			return "", fmt.Errorf("neither $%s nor $HOME is set", env)
		}
		return filepath.Join(home, fallback), nil
	}
	s, err := base("XDG_STATE_HOME", ".local/state")
	if err != nil {
		return "", "", err
	}
	c, err := base("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return "", "", err
	}
	return filepath.Join(s, "poketokenbar"), filepath.Join(c, "poketokenbar"), nil
}

const lockFileName = "helper.lock"

// errAlreadyRunning is returned when another helper holds the lock for the whole wait.
type errAlreadyRunning struct {
	pid  string
	path string
}

func (e errAlreadyRunning) Error() string {
	if e.pid == "" {
		return "another ptb-helper holds " + e.path
	}
	return fmt.Sprintf("another ptb-helper (pid %s) holds %s", e.pid, e.path)
}

// acquireLock takes flock(LOCK_EX|LOCK_NB) on <dir>/helper.lock, retrying every 100 ms for up to
// wait. The kernel releases it when the process dies, so there is no stale-lock cleanup; the pid
// written into the file is informational only. Closing the returned file releases the lock.
func acquireLock(dir string, wait time.Duration) (*os.File, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			b, _ := os.ReadFile(path)
			f.Close()
			return nil, errAlreadyRunning{pid: strings.TrimSpace(string(b)), path: path}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return f, nil
}

// LowerPriority sets nice 10 on every thread of the process. On Linux setpriority(PRIO_PROCESS, 0)
// changes the calling thread only, and threads inherit the value of the thread that creates them,
// so each existing task is set; threads created later inherit 10.
func LowerPriority() error {
	ents, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
	}
	var first error
	for _, e := range ents {
		tid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, 10); err != nil && first == nil {
			first = err
		}
	}
	return first
}
