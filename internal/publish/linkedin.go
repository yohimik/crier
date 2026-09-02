package publish

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// LinkedInPartSize is the byte-range size LinkedIn's video upload uses.
const LinkedInPartSize = 4 << 20

// RestliVersion is the protocol version header every LinkedIn REST call needs.
const RestliVersion = "2.0.0"

// LinkedIn posts to a person's or an organisation's feed.
//
// Two headers are mandatory on every call — LinkedIn-Version and
// X-Restli-Protocol-Version — and omitting either gets a 426 that says nothing
// useful, so they are set in one place.
type LinkedIn struct {
	cfg    config.LinkedIn
	client *httpx.Client
	log    zerolog.Logger
}

func newLinkedIn(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.LinkedIn
	if err := require(c.APIBaseURL, "publish.linkedin.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.linkedin.token"); err != nil {
		return nil, err
	}
	if err := require(c.AuthorURN, "publish.linkedin.author-urn"); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(c.AuthorURN, "urn:li:") {
		return nil, fmt.Errorf("publish.linkedin.author-urn should look like urn:li:person:XXXX "+
			"or urn:li:organization:NNNN, got %q", c.AuthorURN)
	}
	if err := require(c.Version, "publish.linkedin.version"); err != nil {
		return nil, err
	}
	return &LinkedIn{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (l *LinkedIn) Name() string { return "linkedin" }

// LinkedInImageMax is how many images one multi-image post holds.
//
// The API also sets a minimum of two, and a single image uses a different
// content shape entirely, so a batch of one takes the single-image path rather
// than a multiImage of one.
const LinkedInImageMax = 20

// Needs implements Publisher.
func (l *LinkedIn) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageAndVideo, MaxAttachments: LinkedInImageMax}
}

// LinkedInCommentaryMax is LinkedIn's hard cap on a post's commentary, in
// characters, counted after escaping. v1.0.0's graduation collected a whole
// rc train into the caption's changelog and the post was refused at 4408:
// "ShareCommentary text length exceeded the maximum allowed (4000)".
const LinkedInCommentaryMax = 4000

// commentary escapes the caption and, when even a caption worth posting runs
// past LinkedIn's cap, cuts it at the last whole line that fits and says so.
// A trimmed changelog reaches people; a refused post reaches nobody.
func (l *LinkedIn) commentary(caption string) string {
	c := escapeLittleText(caption)
	if len([]rune(c)) <= LinkedInCommentaryMax {
		return c
	}
	marker := "\n…"
	runes := []rune(c)
	cut := LinkedInCommentaryMax - len([]rune(marker))
	kept := string(runes[:cut])
	if nl := strings.LastIndex(kept, "\n"); nl > 0 {
		kept = kept[:nl]
	}
	l.log.Warn().Int("length", len(runes)).Int("max", LinkedInCommentaryMax).
		Msg("the caption runs past linkedin's commentary cap and was cut at the last line that fits")
	return kept + marker
}

// escapeLittleText escapes the characters LinkedIn's "little text format"
// treats as markup. Commentary is parsed, not displayed: an unescaped
// parenthesis or pipe is a syntax token, and rc.15's post showed only the
// text before its first one, swallowing the link, the install command and
// every hashtag behind it. The hash stays bare on purpose, because escaping
// it turns hashtags into plain text.
func escapeLittleText(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch r {
		case '\\', '|', '{', '}', '@', '[', ']', '(', ')', '<', '>', '*', '_', '~':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// encodeURN percent-encodes a URN for a path segment. Rest.li answers a raw
// colon in the path with 400 "Syntax exception in path variables" — rc.13's
// clip and album both died on exactly that, after every upload had already
// succeeded — so the colons travel as %3A, the way every path example in
// LinkedIn's own docs spells them. The joiner keeps segments as given.
func encodeURN(urn string) string { return strings.ReplaceAll(urn, ":", "%3A") }

// rest starts a request with the headers LinkedIn insists on.
func (l *LinkedIn) rest(method string, segments ...string) *httpx.Builder {
	return httpx.NewRequest(method, l.cfg.APIBaseURL, segments...).
		Bearer(l.cfg.Token).
		Header("LinkedIn-Version", l.cfg.Version).
		Header("X-Restli-Protocol-Version", RestliVersion)
}

type liImageInit struct {
	Value struct {
		UploadURL string `json:"uploadUrl"`
		Image     string `json:"image"`
	} `json:"value"`
}

type liVideoInit struct {
	Value struct {
		Video              string `json:"video"`
		UploadToken        string `json:"uploadToken"`
		UploadInstructions []struct {
			UploadURL string `json:"uploadUrl"`
			FirstByte int64  `json:"firstByte"`
			LastByte  int64  `json:"lastByte"`
		} `json:"uploadInstructions"`
	} `json:"value"`
}

type liVideoStatus struct {
	Status string `json:"status"`
}

type liPost struct {
	ID string `json:"id"`
}

// Publish uploads the media and creates the post.
//
// One image and several are two different posts as far as LinkedIn is
// concerned: content.media carries one, content.multiImage carries two to
// twenty, and a multiImage of one is refused. So the shape is chosen by how
// many pages the batch holds rather than by a flag.
func (l *LinkedIn) Publish(ctx context.Context, in Input) (Result, error) {
	arts := in.Sequence()
	if len(arts) > LinkedInImageMax {
		return Result{}, fmt.Errorf("a linkedin multi-image post holds %d images and this one has %d",
			LinkedInImageMax, len(arts))
	}

	content := map[string]any{}
	var urns []string
	switch {
	case len(arts) > 1:
		images := make([]map[string]any, 0, len(arts))
		for n, a := range arts {
			urn, err := l.uploadImage(ctx, a)
			if err != nil {
				return Result{}, fmt.Errorf("image %d of %d: %w", n+1, len(arts), err)
			}
			urns = append(urns, urn)
			images = append(images, map[string]any{"id": urn})
		}
		content["multiImage"] = map[string]any{"images": images}
	default:
		var (
			urn string
			err error
		)
		if in.Artifact.Kind == render.KindVideo {
			urn, err = l.uploadVideo(ctx, in.Artifact)
		} else {
			urn, err = l.uploadImage(ctx, in.Artifact)
		}
		if err != nil {
			return Result{}, err
		}
		urns = append(urns, urn)
		content["media"] = map[string]any{"id": urn}
	}

	body := map[string]any{
		"author":                    l.cfg.AuthorURN,
		"commentary":                l.commentary(in.Caption),
		"visibility":                "PUBLIC",
		"distribution":              map[string]any{"feedDistribution": "MAIN_FEED"},
		"content":                   content,
		"lifecycleState":            "PUBLISHED",
		"isReshareDisabledByAuthor": false,
	}

	resp, err := l.client.NoRetry().Send(ctx, l.rest(http.MethodPost, "rest/posts").JSON(body))
	if err != nil {
		return Result{}, fmt.Errorf("creating the post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// LinkedIn returns the post's URN in a header and, sometimes, in the body.
	id := resp.Header.Get("x-restli-id")
	if id == "" {
		var out liPost
		_ = decodeJSON(resp.Body, &out)
		id = out.ID
	}
	return Result{
		ID:    id,
		URL:   postURL(id),
		Extra: map[string]string{"mediaUrn": strings.Join(urns, ",")},
	}, nil
}

func postURL(id string) string {
	if id == "" {
		return ""
	}
	return "https://www.linkedin.com/feed/update/" + id
}

// uploadImage runs the two-step image flow: ask for an upload URL, PUT the
// bytes there.
func (l *LinkedIn) uploadImage(ctx context.Context, a render.Artifact) (string, error) {
	var init liImageInit
	req := l.rest(http.MethodPost, "rest/images").
		Query("action", "initializeUpload").
		JSON(map[string]any{
			"initializeUploadRequest": map[string]any{"owner": l.cfg.AuthorURN},
		})
	if err := l.client.JSON(ctx, req, &init); err != nil {
		return "", fmt.Errorf("starting the image upload: %w", err)
	}
	if init.Value.UploadURL == "" || init.Value.Image == "" {
		return "", fmt.Errorf("linkedin returned no upload url")
	}

	put := httpx.NewRequest(http.MethodPut, init.Value.UploadURL).
		Bearer(l.cfg.Token).
		File(a.ContentType, a.Path)
	if err := l.client.Discard(ctx, put); err != nil {
		return "", fmt.Errorf("uploading the image: %w", err)
	}
	if err := l.awaitImage(ctx, init.Value.Image); err != nil {
		return "", err
	}
	return init.Value.Image, nil
}

// awaitImage waits until LinkedIn reports the image as available.
//
// The images documentation never says to wait, but the posts documentation
// says what happens to those who do not: the post is accepted, sits in
// PUBLISH_REQUESTED, and can end PUBLISH_FAILED with nothing visible and no
// error on any call crier made. Waiting for AVAILABLE first, the way the
// video flow already does, is the only place that failure can be caught.
func (l *LinkedIn) awaitImage(ctx context.Context, urn string) error {
	err := httpx.Poll(ctx, 2*time.Second, 2*time.Minute, func(ctx context.Context) (bool, error) {
		var st liVideoStatus
		if err := l.client.JSON(ctx, l.rest(http.MethodGet, "rest/images", encodeURN(urn)), &st); err != nil {
			return false, err
		}
		switch strings.ToUpper(st.Status) {
		case "AVAILABLE":
			return true, nil
		case "PROCESSING_FAILED":
			return false, fmt.Errorf("linkedin could not process the image")
		default:
			l.log.Debug().Str("status", st.Status).Msg("waiting for linkedin to process the image")
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("waiting for the image: %w", err)
	}
	return nil
}

// uploadVideo runs the multi-part video flow.
//
// Each part's ETag has to come back in the finalize call, in order: LinkedIn
// reassembles the file from them, and a missing or reordered tag produces a
// video that uploads and then never becomes available.
func (l *LinkedIn) uploadVideo(ctx context.Context, a render.Artifact) (string, error) {
	var init liVideoInit
	req := l.rest(http.MethodPost, "rest/videos").
		Query("action", "initializeUpload").
		JSON(map[string]any{
			"initializeUploadRequest": map[string]any{
				"owner":           l.cfg.AuthorURN,
				"fileSizeBytes":   a.Size,
				"uploadCaptions":  false,
				"uploadThumbnail": false,
			},
		})
	if err := l.client.JSON(ctx, req, &init); err != nil {
		return "", fmt.Errorf("starting the video upload: %w", err)
	}
	if init.Value.Video == "" || len(init.Value.UploadInstructions) == 0 {
		return "", fmt.Errorf("linkedin returned no upload instructions")
	}

	etags := make([]string, 0, len(init.Value.UploadInstructions))
	for i, ins := range init.Value.UploadInstructions {
		c := Chunk{Index: i, Start: ins.FirstByte, End: ins.LastByte, Size: ins.LastByte - ins.FirstByte + 1}
		data, err := readChunk(a.Path, c)
		if err != nil {
			return "", err
		}
		put := httpx.NewRequest(http.MethodPut, ins.UploadURL).
			Bearer(l.cfg.Token).
			Bytes("application/octet-stream", data)
		resp, err := l.client.Send(ctx, put)
		if err != nil {
			return "", fmt.Errorf("uploading part %d: %w", i, err)
		}
		etag := strings.Trim(resp.Header.Get("ETag"), `"`)
		_ = resp.Body.Close()
		if etag == "" {
			return "", fmt.Errorf("part %d came back without an ETag, so the video cannot be finalized", i)
		}
		etags = append(etags, etag)
		l.log.Debug().Int("part", i).Int64("bytes", c.Size).Msg("uploaded a video part to linkedin")
	}

	fin := l.rest(http.MethodPost, "rest/videos").
		Query("action", "finalizeUpload").
		JSON(map[string]any{
			"finalizeUploadRequest": map[string]any{
				"video":           init.Value.Video,
				"uploadToken":     init.Value.UploadToken,
				"uploadedPartIds": etags,
			},
		})
	if err := l.client.Discard(ctx, fin); err != nil {
		return "", fmt.Errorf("finalizing the video: %w", err)
	}

	if err := l.awaitVideo(ctx, init.Value.Video); err != nil {
		return "", err
	}
	return init.Value.Video, nil
}

// awaitVideo waits until LinkedIn reports the video as available.
func (l *LinkedIn) awaitVideo(ctx context.Context, urn string) error {
	err := httpx.Poll(ctx, 3*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		var st liVideoStatus
		if err := l.client.JSON(ctx, l.rest(http.MethodGet, "rest/videos", encodeURN(urn)), &st); err != nil {
			return false, err
		}
		switch strings.ToUpper(st.Status) {
		case "AVAILABLE":
			return true, nil
		case "PROCESSING_FAILED":
			return false, fmt.Errorf("linkedin could not process the video")
		default:
			l.log.Debug().Str("status", st.Status).Msg("waiting for linkedin to process the video")
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("waiting for the video: %w", err)
	}
	return nil
}

// Ping reads the member the token belongs to.
//
// LinkedIn is the one platform with no identity endpoint a posting token is
// guaranteed to reach. /v2/me needs r_liteprofile and /v2/userinfo needs the
// OpenID scopes, while posting needs only w_member_social — so a perfectly good
// posting token may be refused by every endpoint that could name its owner.
//
// The two refusals are told apart rather than lumped together: 401 means the
// token is not valid and the setup is broken; 403 means it is valid and merely
// cannot see a profile, which is a working configuration and is reported as
// one, with a note saying how to get the name as well.
//
// Verified against the LinkedIn docs on 2026-09-01: userinfo_endpoint is
// https://api.linkedin.com/v2/userinfo in the OpenID discovery document, and
// its scopes_supported are openid, profile and email.
func (l *LinkedIn) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		Sub  string `json:"sub"`
		Name string `json:"name"`
	}
	// Without the LinkedIn-Version header: it belongs to the versioned /rest
	// API, and /v2 answers 426 when it is sent.
	req := httpx.NewRequest(http.MethodGet, l.cfg.APIBaseURL, "v2/userinfo").
		Bearer(l.cfg.Token).
		Header("X-Restli-Protocol-Version", RestliVersion)
	err := l.client.JSON(ctx, req, &out)
	id := Identity{ID: firstNonEmpty(out.Sub, l.cfg.AuthorURN), Name: out.Name}
	if err != nil {
		var apiErr *httpx.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			return Identity{}, err
		}
		id = Identity{
			ID: l.cfg.AuthorURN,
			Note: "the token can post but cannot read a profile; " +
				"add the openid and profile scopes for crier ping to name the account",
		}
	}

	// The identity endpoint proves the token parses, not that it can post:
	// rc.12's token answered userinfo happily and every upload was refused
	// with 403 partnerApiImagesExternal, so ping said ok about a release
	// that could not announce. The probe asks for an upload slot exactly
	// the way a post would and abandons it — nothing is uploaded, nothing
	// becomes visible, and LinkedIn expires the lease on its own.
	var lease liImageInit
	probe := l.rest(http.MethodPost, "rest/images").
		Query("action", "initializeUpload").
		JSON(map[string]any{
			"initializeUploadRequest": map[string]any{"owner": l.cfg.AuthorURN},
		})
	if err := l.client.NoRetry().JSON(ctx, probe, &lease); err != nil {
		return Identity{}, fmt.Errorf("the token cannot upload media, so a post would fail: %w", err)
	}
	return id, nil
}
