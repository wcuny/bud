package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

const defaultPort = 11112

func runUI(port int, noOpen bool) error {
	if alreadyRunning(port, noOpen) {
		return nil
	}
	st, err := openStore()
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fail(fmt.Errorf("cannot listen on port %d: %v (try --port)", port, err))
	}
	srv := &http.Server{Handler: newHandler(st, port), ReadHeaderTimeout: 5 * time.Second}

	// The pid file lets `bud ui --stop` find this server.
	if pf, err := uiPidFile(port); err == nil {
		if os.WriteFile(pf, []byte(strconv.Itoa(os.Getpid())), 0o600) == nil {
			defer os.Remove(pf)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Fprintf(os.Stderr, "bud: dashboard at %s (Ctrl-C to stop)\n", url)
	if !noOpen {
		openBrowser(url)
	}
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fail(err)
		}
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
	return nil
}

func uiPidFile(port int) (string, error) {
	p, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), fmt.Sprintf("ui-%d.pid", port)), nil
}

// Starts the dashboard as a background process in its own session
func detachUI(port int, noOpen bool) error {
	if alreadyRunning(port, noOpen) {
		return nil
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fail(fmt.Errorf("port %d is not available (another program is using it? try --port): %v", port, err))
	}
	ln.Close()

	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	c := exec.Command(exe, "ui", "--port", strconv.Itoa(port), "--no-open")
	if err := detachAttrs(c); err != nil {
		return fail(err)
	}
	if err := c.Start(); err != nil { // stdio defaults to the null device
		return fail(err)
	}
	exited := make(chan struct{})
	go func() { c.Wait(); close(exited) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			conn.Close()
			break
		}
		select {
		case <-exited:
			return fail(errors.New("the background dashboard exited during startup"))
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fail(errors.New("timed out waiting for the dashboard to start"))
		}
	}
	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Fprintf(os.Stderr, "bud: dashboard running in the background at %s (pid %d); stop it with `bud ui --stop`\n", url, c.Process.Pid)
	if !noOpen {
		openBrowser(url)
	}
	return nil
}

// runningUI reports the pid of a dashboard already serving this port. It
// needs a pid file whose process is alive and a live listener on the port, so
// a stale pid file (crash, reused pid) is never trusted.
func runningUI(port int) (int, bool) {
	pf, err := uiPidFile(port)
	if err != nil {
		return 0, false
	}
	b, err := os.ReadFile(pf)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	if proc, err := os.FindProcess(pid); err != nil || proc.Signal(syscall.Signal(0)) != nil {
		return 0, false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return 0, false
	}
	conn.Close()
	return pid, true
}

func alreadyRunning(port int, noOpen bool) bool {
	pid, ok := runningUI(port)
	if !ok {
		return false
	}
	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Fprintf(os.Stderr, "bud: the dashboard is already running at %s (pid %d); stop it with `bud ui --stop`\n", url, pid)
	if !noOpen {
		openBrowser(url)
	}
	return true
}

// stopUI stops the dashboard started on this port (foreground or detached).
func stopUI(port int) error {
	pf, err := uiPidFile(port)
	if err != nil {
		return fail(err)
	}
	pid, ok := runningUI(port)
	if !ok {
		if _, err := os.Stat(pf); err == nil {
			os.Remove(pf)
			return fail(fmt.Errorf("no dashboard is running on port %d (removed a stale pid file)", port))
		}
		return fail(fmt.Errorf("no dashboard is running on port %d", port))
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		err = proc.Signal(syscall.SIGTERM)
	}
	if err != nil {
		return fail(fmt.Errorf("could not stop pid %d: %v", pid, err))
	}
	for i := 0; i < 30; i++ { // the server removes its pid file on exit
		if _, err := os.Stat(pf); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "bud: stopped the dashboard on port %d\n", port)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fail(fmt.Errorf("sent SIGTERM to pid %d but it has not exited yet", pid))
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "bud: could not open a browser:", err)
		return
	}
	go c.Wait()
}

type api struct{ st *Store }

func newHandler(st *Store, port int) http.Handler {
	a := &api{st: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/commands", a.list)
	mux.HandleFunc("POST /api/commands", a.create)
	mux.HandleFunc("PUT /api/commands/{id}", a.update)
	mux.HandleFunc("DELETE /api/commands/{id}", a.remove)
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return guard(port, mux)
}

// guard blocks DNS rebinding and cross site requests. This API can write
// commands that the user later executes, so it must not be reachable from
// arbitrary web pages.
func guard(port int, next http.Handler) http.Handler {
	hosts := map[string]bool{
		fmt.Sprintf("localhost:%d", port): true,
		fmt.Sprintf("127.0.0.1:%d", port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if (r.Method == http.MethodPost || r.Method == http.MethodPut) &&
			!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type scopeOpt struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	all, err := a.st.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tagSet, scopeSet := map[string]bool{}, map[string]bool{}
	for _, c := range all {
		for _, t := range c.Tags {
			tagSet[t] = true
		}
		if c.Scope != "" {
			scopeSet[c.Scope] = true
		}
	}
	tags := []string{}
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	paths := []string{}
	for s := range scopeSet {
		paths = append(paths, s)
	}
	sort.Strings(paths)
	scopes := []scopeOpt{{"global", "global"}}
	for _, p := range paths {
		scopes = append(scopes, scopeOpt{p, scopeLabel(p)})
	}

	q := r.URL.Query()
	tag := strings.ToLower(strings.TrimSpace(q.Get("tag")))
	scope := q.Get("scope")
	var keep []Command
	for _, c := range all {
		if tag != "" && !hasTag(c, tag) {
			continue
		}
		if scope == "global" && c.Scope != "" {
			continue
		}
		if scope != "" && scope != "global" && c.Scope != scope {
			continue
		}
		keep = append(keep, c)
	}
	cmds := rank(keep, q.Get("q"), "")
	if cmds == nil {
		cmds = []Command{}
	}
	writeJSON(w, 200, map[string]any{"commands": cmds, "tags": tags, "scopes": scopes})
}

type payload struct {
	Name  string   `json:"name"`
	Cmd   string   `json:"cmd"`
	Tip   string   `json:"tip"`
	Tags  []string `json:"tags"`
	Scope string   `json:"scope"`
}

func decode(w http.ResponseWriter, r *http.Request) (*Command, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), 400)
		return nil, false
	}
	c := &Command{
		Name:  strings.TrimSpace(p.Name),
		Cmd:   p.Cmd,
		Tip:   p.Tip,
		Tags:  normalizeTags(p.Tags),
		Scope: strings.TrimSpace(p.Scope),
	}
	if c.Scope != "" {
		c.Scope = filepath.Clean(c.Scope)
	}
	if strings.TrimSpace(c.Cmd) == "" {
		http.Error(w, "command is required", 400)
		return nil, false
	}
	if c.Name == "" {
		c.Name = slugify(c.Cmd)
	}
	return c, true
}

func (a *api) create(w http.ResponseWriter, r *http.Request) {
	c, ok := decode(w, r)
	if !ok {
		return
	}
	if err := a.st.Create(c); err != nil {
		if isUnique(err) {
			http.Error(w, "that name already exists in this scope", 409)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 201, c)
}

func (a *api) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	c, ok := decode(w, r)
	if !ok {
		return
	}
	c.ID = id
	if err := a.st.Update(c); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "not found", 404)
		case isUnique(err):
			http.Error(w, "that name already exists in this scope", 409)
		default:
			http.Error(w, err.Error(), 500)
		}
		return
	}
	writeJSON(w, 200, c)
}

func (a *api) remove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if err := a.st.Delete(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}