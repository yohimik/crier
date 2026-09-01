package publish

import (
	"context"
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

// Needs implements Publisher.
func (l *LinkedIn) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageAndVideo}
}

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
func (l *LinkedIn) Publish(ctx context.Context, in Input) (Result, error) {
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

	body := map[string]any{
		"author":                    l.cfg.AuthorURN,
		"commentary":                in.Caption,
		"visibility":                "PUBLIC",
		"distribution":              map[string]any{"feedDistribution": "MAIN_FEED"},
		"content":                   map[string]any{"media": map[string]any{"id": urn}},
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
		Extra: map[string]string{"mediaUrn": urn},
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
	return init.Value.Image, nil
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
		if err := l.client.JSON(ctx, l.rest(http.MethodGet, "rest/videos", urn), &st); err != nil {
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
