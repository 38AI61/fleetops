package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Restart and shutdown budgets (GF-349).
const (
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
	// resetAfter is the uptime that clears the backoff, so a child that ran for
	// a while restarts quickly after its next failure.
	resetAfter      = 60 * time.Second
	childStopBudget = 10 * time.Second
)

// child is one supervised process. path is absolute; main resolves it beside
// this executable (GF-349), so the container runs only the binaries it was built with.
type child struct {
	name string
	path string
	args []string
	env  []string
}

// running is the live process of one child. err is the Wait result and is
// readable once exited is closed.
type running struct {
	cmd    *exec.Cmd
	exited chan struct{}
	err    error
}

// supervisor starts children, restarts them with backoff, and stops them in
// reverse order (GF-349). now and sleep are injected for tests.
type supervisor struct {
	stop  chan struct{}
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	mu       sync.Mutex
	procs    map[string]*running
	order    []string
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newSupervisor() *supervisor {
	return &supervisor{
		stop:  make(chan struct{}),
		now:   time.Now,
		sleep: sleepCtx,
		procs: map[string]*running{},
	}
}

// sleepCtx waits for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// resolve returns the path of name beside this executable (GF-349).
func resolve(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate supervisor: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), name), nil
}

// start supervises c in its own goroutine. It returns at once. The goroutine
// restarts c after each exit, unless shutdown has begun.
func (s *supervisor) start(ctx context.Context, c child) {
	s.mu.Lock()
	s.order = append(s.order, c.name)
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx, c)
	}()
}

func (s *supervisor) run(ctx context.Context, c child) {
	delay := initialBackoff
	for {
		started := s.now()
		r, err := s.spawn(c)
		if err != nil {
			log.Printf("fleetops-cloud: start %s: %v", c.name, err)
		} else {
			s.setRunning(c.name, r)
			// A shutdown that began before setRunning missed this process, so stop it here.
			if s.isStopping() {
				stopProcess(c.name, r)
				return
			}
			<-r.exited
			if s.isStopping() {
				return
			}
			log.Printf("fleetops-cloud: %s exited (%v); restarting", c.name, r.err)
		}
		if s.isStopping() {
			return
		}
		if s.now().Sub(started) >= resetAfter {
			delay = initialBackoff
		}
		if err := s.sleep(ctx, delay); err != nil {
			return
		}
		if s.isStopping() {
			return
		}
		delay = min(delay*2, maxBackoff)
	}
}

// spawn starts c with its output relayed through a prefix (GF-349).
func (s *supervisor) spawn(c child) (*running, error) {
	//nolint:gosec // fixed child binary names resolved beside this executable
	cmd := exec.Command(c.path, c.args...)
	cmd.Env = c.env
	cmd.Stdout = &linePrefixer{prefix: "[" + c.name + "] ", dst: os.Stdout}
	cmd.Stderr = &linePrefixer{prefix: "[" + c.name + "] ", dst: os.Stderr}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &running{cmd: cmd, exited: make(chan struct{})}
	go func() {
		r.err = cmd.Wait()
		close(r.exited)
	}()
	return r, nil
}

func (s *supervisor) setRunning(name string, r *running) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.procs[name] = r
}

func (s *supervisor) isStopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

// shutdown stops children in reverse start order: SIGTERM, then SIGKILL after
// childStopBudget (GF-349). Restarts are suppressed first, so an exit during
// shutdown is final.
func (s *supervisor) shutdown() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.mu.Lock()
	order := append([]string(nil), s.order...)
	s.mu.Unlock()
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		s.mu.Lock()
		r := s.procs[name]
		s.mu.Unlock()
		if r == nil {
			continue
		}
		stopProcess(name, r)
	}
	s.wg.Wait()
}

// stopProcess signals r and waits for its exit. The wait is bounded by
// childStopBudget, after which the process is killed.
func stopProcess(name string, r *running) {
	select {
	case <-r.exited:
		return
	default:
	}
	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-r.exited:
		log.Printf("fleetops-cloud: %s stopped", name)
	case <-time.After(childStopBudget):
		log.Printf("fleetops-cloud: %s did not stop in %s; killing", name, childStopBudget)
		_ = r.cmd.Process.Kill()
		<-r.exited
	}
}

// linePrefixer writes each complete line to dst with prefix (GF-349). Logs go
// to stdout and stderr only; no files.
type linePrefixer struct {
	prefix string
	dst    *os.File

	mu  sync.Mutex
	buf []byte
}

func (p *linePrefixer) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		_, _ = fmt.Fprintf(p.dst, "%s%s\n", p.prefix, p.buf[:i])
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}
