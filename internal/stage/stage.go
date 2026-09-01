// Package stage makes a rendered file reachable at a public URL.
//
// Instagram and TikTok will not take bytes: they take a URL and fetch it
// themselves, from their own servers. That one requirement is the whole reason
// this package exists, and it is why a laptop publishing to Instagram needs
// either an object store, a pre-hosted file, or a tunnel back to itself.
package stage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

// Mode names a staging strategy.
type Mode string

const (
	// ModeNone stages nothing: crier can only publish to platforms that accept
	// the bytes directly.
	ModeNone Mode = "none"
	// ModeS3 uploads to an S3-compatible bucket.
	ModeS3 Mode = "s3"
	// ModeServer serves the file from a local HTTP server, optionally through a
	// tunnel.
	ModeServer Mode = "server"
	// ModeURL uses a URL the operator has already published the file at.
	ModeURL Mode = "url"
)

// Asset is a file to be staged.
type Asset struct {
	// Path is the file on disk.
	Path string
	// ContentType is its MIME type.
	ContentType string
	// Name is the file name the staged copy should carry. Empty uses the base
	// name of Path.
	Name string
	// Size is the file's length in bytes.
	Size int64
}

// Object is a staged file.
type Object struct {
	// URL is where the platform can fetch it.
	URL string
	// Remove deletes the staged copy. It is never nil.
	Remove func(ctx context.Context) error
}

// Stager publishes a file at a URL.
type Stager interface {
	// Name is the mode's name, for logs and the dry-run report.
	Name() string
	// Stage makes the asset reachable and returns where.
	Stage(ctx context.Context, a Asset) (*Object, error)
	// Close releases whatever the stager is holding: a listening socket, a
	// tunnel subprocess. It is called once, after every publisher has finished.
	Close(ctx context.Context) error
}

// Pinger is a Stager whose configuration can be checked without staging
// anything.
//
// It is optional because most modes have nothing to check: `none` does
// nothing, `url` is a string the operator vouched for, and `server` binds a
// socket rather than holding a credential. Only the object store has something
// that can be wrong in a way a request would reveal, so only it implements
// this.
type Pinger interface {
	// Ping verifies the staging configuration and describes what it reached.
	Ping(ctx context.Context) (string, error)
}

// ErrNoStaging is returned when a platform needs a URL and nothing is set up
// to produce one.
var ErrNoStaging = errors.New("this platform can only be given a URL, and stage.mode is none")

// noRemove is the cleanup of something crier did not create.
func noRemove(context.Context) error { return nil }

// None stages nothing. It is the null object the pipeline holds when no
// staging is configured, so no call site has to nil-check the stager.
type None struct{}

// Name implements Stager.
func (None) Name() string { return string(ModeNone) }

// Stage implements Stager by refusing, with the error that says what to do.
func (None) Stage(context.Context, Asset) (*Object, error) { return nil, ErrNoStaging }

// Close implements Stager.
func (None) Close(context.Context) error { return nil }

// Options configures New.
type Options struct {
	Config config.Stage
	Logger zerolog.Logger
	// Client is used by the tunnel to poll ngrok's local agent API.
	Client *httpx.Client
	// Dir is where the local server serves from. Empty makes one.
	Dir string
}

// New builds the stager the configuration asks for.
func New(o Options) (Stager, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(o.Config.Mode))) {
	case "", ModeNone:
		return None{}, nil
	case ModeURL:
		return NewURL(o.Config.URL), nil
	case ModeS3:
		return NewS3(o.Config.S3, o.Logger)
	case ModeServer:
		return NewServer(o.Config.Server, o.Logger, o.Client, o.Dir)
	default:
		return nil, fmt.Errorf("unknown stage mode %q", o.Config.Mode)
	}
}

// URL hands out a URL the operator has already published the file at.
//
// It is the escape hatch for a setup crier knows nothing about: a CDN, a
// static site, a bucket somebody else uploads to.
type URL struct{ url string }

// NewURL builds a pre-hosted URL stager.
func NewURL(u string) *URL { return &URL{url: u} }

// Name implements Stager.
func (u *URL) Name() string { return string(ModeURL) }

// Stage implements Stager. It does not check that the URL points at the asset:
// only the operator knows that, and saying so is what choosing this mode means.
func (u *URL) Stage(context.Context, Asset) (*Object, error) {
	if u.url == "" {
		return nil, fmt.Errorf("stage.mode is url but stage.url is empty")
	}
	return &Object{URL: u.url, Remove: noRemove}, nil
}

// Close implements Stager.
func (u *URL) Close(context.Context) error { return nil }
