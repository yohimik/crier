package stage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

// Server serves staged files from crier's own machine.
//
// It is the mode for a laptop: the file never leaves the machine, and a tunnel
// gives the platform's fetcher a way in. Without a tunnel the operator has to
// say what public URL the listener is reachable at, because crier cannot know
// what is in front of it.
type Server struct {
	cfg    config.Server
	log    zerolog.Logger
	client *httpx.Client

	dir    string
	ownDir bool

	mu    sync.Mutex
	files map[string]string // path element -> file on disk

	startOnce sync.Once
	startErr  error
	base      string
	ln        net.Listener
	srv       *http.Server
	served    chan struct{}
	tunnel    Tunnel
}

// NewServer builds the local staging server. Nothing is listening yet: the
// listener opens on the first Stage, so a dry run costs no socket.
func NewServer(cfg config.Server, log zerolog.Logger, client *httpx.Client, dir string) (*Server, error) {
	s := &Server{
		cfg:    cfg,
		log:    log,
		client: client,
		dir:    dir,
		files:  map[string]string{},
		served: make(chan struct{}),
	}
	if s.dir == "" {
		tmp, err := os.MkdirTemp("", "crier-stage-")
		if err != nil {
			return nil, fmt.Errorf("creating the staging directory: %w", err)
		}
		s.dir, s.ownDir = tmp, true
	}
	return s, nil
}

// Name implements Stager.
func (s *Server) Name() string { return string(ModeServer) }

// Addr is the address the server is listening on, empty before it starts. It
// exists for the tests, which need the real port after listening on :0.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// BaseURL is the public URL prefix, empty before the server starts.
func (s *Server) BaseURL() string { return s.base }

// Stage copies the asset into the served directory and returns its URL.
func (s *Server) Stage(ctx context.Context, a Asset) (*Object, error) {
	if err := s.start(ctx); err != nil {
		return nil, err
	}

	name := a.Name
	if name == "" {
		name = filepath.Base(a.Path)
	}
	element := randomToken() + "-" + sanitiseName(name)
	dst := filepath.Join(s.dir, element)
	if err := copyFile(a.Path, dst); err != nil {
		return nil, fmt.Errorf("staging %s: %w", a.Path, err)
	}

	s.mu.Lock()
	s.files[element] = dst
	s.mu.Unlock()

	link := strings.TrimRight(s.base, "/") + "/" + element
	s.log.Debug().Str("url", link).Str("file", dst).Msg("staged a file on the local server")

	return &Object{
		URL: link,
		Remove: func(context.Context) error {
			s.mu.Lock()
			delete(s.files, element)
			s.mu.Unlock()
			if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		},
	}, nil
}

// start opens the listener and, when one is configured, the tunnel.
func (s *Server) start(ctx context.Context) error {
	s.startOnce.Do(func() { s.startErr = s.startLocked(ctx) })
	return s.startErr
}

func (s *Server) startLocked(ctx context.Context) error {
	listen := s.cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	// Through a ListenConfig so the caller's context can abort a listen that
	// blocks — and so the socket is not opened by a bare package-level call
	// that nothing can cancel.
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", listen, err)
	}
	s.ln = ln
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		defer close(s.served)
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error().Err(err).Msg("the staging server stopped")
		}
	}()
	s.log.Debug().Str("addr", ln.Addr().String()).Msg("staging server listening")

	base, err := s.publicBase(ctx, ln)
	if err != nil {
		_ = s.srv.Close()
		return err
	}
	s.base = base
	s.log.Info().Str("url", base).Msg("staged files are reachable at this address")
	return nil
}

// publicBase is the URL prefix a platform's fetcher can reach, from the
// tunnel or from the operator.
func (s *Server) publicBase(ctx context.Context, ln net.Listener) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Tunnel.Mode))
	if mode == "" || mode == string(TunnelNone) {
		base := strings.TrimRight(s.cfg.PublicURL, "/")
		if base == "" {
			return "", fmt.Errorf("stage.server.public-url is empty and no tunnel is configured, " +
				"so there is no address a platform could fetch from")
		}
		return base, nil
	}

	tunnel, err := NewTunnel(TunnelOptions{
		Config: s.cfg.Tunnel,
		Logger: s.log,
		Client: s.client,
	})
	if err != nil {
		return "", err
	}
	s.tunnel = tunnel
	url, err := tunnel.Start(ctx, ln.Addr().String())
	if err != nil {
		return "", err
	}
	return strings.TrimRight(url, "/"), nil
}

// ServeHTTP serves exactly the files that were staged.
//
// Serving the directory instead would let a crafted path walk out of it, and
// the whole point of this server is that it is exposed to the internet.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	element := strings.TrimPrefix(r.URL.Path, "/")
	s.mu.Lock()
	path, ok := s.files[element]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.log.Debug().Str("path", r.URL.Path).Str("from", r.RemoteAddr).Msg("serving a staged file")
	http.ServeFile(w, r, path)
}

// Close stops the tunnel, then the listener, then removes the files.
//
// The order matters: stopping the listener first would leave the tunnel
// pointing at a closed port, and on a slow shutdown the platform would get a
// connection refused rather than a 404.
func (s *Server) Close(ctx context.Context) error {
	var errs []error
	if s.tunnel != nil {
		if err := s.tunnel.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
		s.tunnel = nil
	}
	if s.srv != nil {
		timeout := config.Duration(s.cfg.ShutdownTimeout)
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
		if err := s.srv.Shutdown(shutdownCtx); err != nil {
			// A shutdown that will not finish still has to give the port back.
			_ = s.srv.Close()
			errs = append(errs, err)
		}
		cancel()
		<-s.served
		s.srv = nil
	}

	s.mu.Lock()
	files := s.files
	s.files = map[string]string{}
	s.mu.Unlock()
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if s.ownDir {
		if err := os.RemoveAll(s.dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// copyFile duplicates a file, so the artifact's own temporary directory can be
// cleaned up independently of the server's.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return nil
}
