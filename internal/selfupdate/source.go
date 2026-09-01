package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"
)

// DefaultAPIURL is GitHub's API. A GitHub Enterprise install is another host,
// which is why it is a field rather than a constant in the request.
const DefaultAPIURL = "https://api.github.com"

const (
	// maxListBody bounds one page of the releases listing. A hundred releases
	// with their assets is well under this; anything past it is not an answer
	// worth parsing.
	maxListBody = 8 << 20
	// maxPages caps the walk back through the listing. GitHub returns releases
	// newest first, so three pages of a hundred is far more history than a
	// "which version is current" question needs.
	maxPages = 3
	// listTimeout is the request timeout of the default client.
	listTimeout = 30 * time.Second
)

// ErrNoRelease means the repository has no release this source would install:
// no tag carrying the prefix, or none outside the prereleases that were asked
// to be skipped.
var ErrNoRelease = errors.New("no matching release")

// Source is where crier's own binaries come from.
type Source struct {
	APIURL    string // default DefaultAPIURL
	Owner     string // default DefaultOwner
	Repo      string // default DefaultRepo
	TagPrefix string // default DefaultTagPrefix
	// Prerelease keeps prereleases in the running. It means "consider them
	// too", not "prefer them": ordering still decides, so a released 1.1.0
	// still beats its own 1.1.0-rc.1.
	Prerelease bool
	// Command is the command word this source's failures name.
	Command string
	// Token authenticates the API calls, which only raises the rate limit: the
	// releases of a public repository are readable without one.
	Token  string
	Client *http.Client // default: a 30s-timeout client
	// Log carries the reasons a release was passed over. The zero value
	// discards them.
	Log zerolog.Logger
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string
	Size int64
	// Digest is the checksum GitHub computed, "sha256:<hex>". Older GitHub
	// Enterprise versions do not send one, and it is then empty.
	Digest string
}

// Release is one published version of crier.
type Release struct {
	Tag     string
	Version ccme.Version
	Assets  []Asset
	// Body is the release's notes, as the markdown GitHub stores.
	Body string
	// HTMLURL is the release's page.
	HTMLURL string
}

// Asset finds the binary for a platform.
func (r Release) Asset(goos, goarch string) (Asset, bool) {
	want := AssetName(goos, goarch)
	for _, a := range r.Assets {
		if a.Name == want {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetNames lists what the release does carry, which is what a refusal over a
// missing platform owes the reader.
func (r Release) AssetNames() []string {
	names := make([]string, 0, len(r.Assets))
	for _, a := range r.Assets {
		names = append(names, a.Name)
	}
	return names
}

// The subset of GitHub's release JSON this package reads.
type apiRelease struct {
	TagName    string     `json:"tag_name"`
	Draft      bool       `json:"draft"`
	Prerelease bool       `json:"prerelease"`
	Body       string     `json:"body"`
	HTMLURL    string     `json:"html_url"`
	Assets     []apiAsset `json:"assets"`
}

type apiAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

func (s *Source) api() string {
	if s.APIURL == "" {
		return DefaultAPIURL
	}
	return strings.TrimSuffix(s.APIURL, "/")
}

func (s *Source) owner() string {
	if s.Owner == "" {
		return DefaultOwner
	}
	return s.Owner
}

func (s *Source) repo() string {
	if s.Repo == "" {
		return DefaultRepo
	}
	return s.Repo
}

func (s *Source) prefix() string {
	if s.TagPrefix == "" {
		return DefaultTagPrefix
	}
	return s.TagPrefix
}

// what names this source in the errors it returns.
func (s *Source) what() string { return commandOr(s.Command) }

func (s *Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: listTimeout}
}

// get performs one API call and returns the body and the response header, the
// latter because the listing's pagination lives in Link. A tolerated status is
// an answer rather than a failure: a 404 from the by-tag lookup means the
// version has no release, which is exactly what the lookup asked.
func (s *Source) get(ctx context.Context, url string, tolerate int, what string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("%s: %w", s.what(), err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("%s: %s: %w", s.what(), what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// One byte past the cap, so a body that reaches it is recognised as
	// truncated here rather than surfacing further down as a JSON syntax error
	// about a document that was fine until it was cut.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxListBody+1))
	if err != nil {
		return nil, nil, resp.StatusCode, fmt.Errorf("%s: %s: reading response: %w", s.what(), what, err)
	}
	if len(data) > maxListBody {
		return nil, nil, resp.StatusCode, fmt.Errorf(
			"%s: %s: the response is larger than %d bytes", s.what(), what, int64(maxListBody))
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != tolerate {
		return nil, nil, resp.StatusCode, fmt.Errorf("%s: %s: unexpected status %s: %s",
			s.what(), what, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, resp.Header, resp.StatusCode, nil
}

// Latest is the highest version this source will install.
//
// Highest, not most recent: releases come back newest first, and a patch cut on
// an older line would otherwise look like an upgrade to everyone running the
// newer one.
func (s *Source) Latest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", s.api(), s.owner(), s.repo())
	var (
		best  Release
		found bool
	)
	for page := 0; page < maxPages && url != ""; page++ {
		data, header, _, err := s.get(ctx, url, 0, "listing releases")
		if err != nil {
			return Release{}, err
		}
		var list []apiRelease
		if err := json.Unmarshal(data, &list); err != nil {
			return Release{}, fmt.Errorf("%s: listing releases: %w", s.what(), err)
		}
		for _, raw := range list {
			rel, ok := s.convert(raw)
			if !ok {
				continue
			}
			if !found || rel.Version.Compare(best.Version) > 0 {
				best, found = rel, true
			}
		}
		url = nextLink(header.Get("Link"))
	}
	if !found {
		return Release{}, ErrNoRelease
	}
	s.Log.Debug().Str("tag", best.Tag).Msg(s.what() + ": highest release selected")
	return best, nil
}

// At is one named version, looked up by its tag.
//
// A version nobody published answers ErrNoRelease rather than "you are up to
// date", because the two are entirely different answers to entirely different
// questions.
func (s *Source) At(ctx context.Context, version string) (Release, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	parsed, err := ccme.ParseVersion(version)
	if err != nil {
		return Release{}, fmt.Errorf("%s: %q is not a version: %w", s.what(), version, err)
	}
	tag := s.prefix() + parsed.String()
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", s.api(), s.owner(), s.repo(), tag)
	data, _, status, err := s.get(ctx, url, http.StatusNotFound, "looking up "+tag)
	if err != nil {
		return Release{}, err
	}
	if status == http.StatusNotFound {
		return Release{}, fmt.Errorf("%w: %s", ErrNoRelease, tag)
	}
	var raw apiRelease
	if err := json.Unmarshal(data, &raw); err != nil {
		return Release{}, fmt.Errorf("%s: looking up %s: %w", s.what(), tag, err)
	}
	if raw.Draft {
		return Release{}, fmt.Errorf("%w: %s is a draft", ErrNoRelease, tag)
	}
	return toRelease(raw, parsed), nil
}

// toRelease is the one place an API release becomes ours, so the two lookups
// cannot drift over which fields they carry.
func toRelease(raw apiRelease, version ccme.Version) Release {
	return Release{
		Tag:     raw.TagName,
		Version: version,
		Assets:  assets(raw.Assets),
		Body:    raw.Body,
		HTMLURL: raw.HTMLURL,
	}
}

// convert keeps the releases this source would install and drops the rest,
// saying why at debug level: a release nobody expected to be skipped is
// otherwise invisible.
func (s *Source) convert(raw apiRelease) (Release, bool) {
	if raw.Draft {
		return Release{}, false
	}
	rest, ok := strings.CutPrefix(raw.TagName, s.prefix())
	if !ok {
		return Release{}, false
	}
	version, err := ccme.ParseVersion(rest)
	if err != nil {
		s.Log.Debug().Str("tag", raw.TagName).Msg(s.what() + ": tag carries no version")
		return Release{}, false
	}
	if !s.Prerelease && (raw.Prerelease || version.IsPrerelease()) {
		s.Log.Debug().Str("tag", raw.TagName).Msg(s.what() + ": prerelease skipped; pass --prerelease to include it")
		return Release{}, false
	}
	return toRelease(raw, version), true
}

func assets(raw []apiAsset) []Asset {
	out := make([]Asset, 0, len(raw))
	for _, a := range raw {
		out = append(out, Asset{Name: a.Name, URL: a.URL, Size: a.Size, Digest: a.Digest})
	}
	return out
}

// nextLink reads the next page's URL out of a Link header, which is how the
// GitHub API paginates.
//
// It returns "" on the last page, which is also what an absent or unparseable
// header gives: one page is a complete answer often enough that a malformed
// header is not worth failing over.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 {
			continue
		}
		url := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(url, "<") || !strings.HasSuffix(url, ">") {
			continue
		}
		for _, seg := range segments[1:] {
			if strings.ReplaceAll(strings.TrimSpace(seg), `"`, "") == "rel=next" {
				return url[1 : len(url)-1]
			}
		}
	}
	return ""
}
