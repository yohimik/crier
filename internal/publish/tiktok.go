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

// TikTokVideoLimit is the largest video the Content Posting API accepts.
//
// Verified against TikTok's media transfer guide, 2026-09-01: 4GB total, at
// most 1000 chunks, each 5MB to 64MB except the last, which may run to 128MB
// to absorb the trailing bytes.
const TikTokVideoLimit = 4 << 30

// TikTokFinalChunkLimit is how large the last chunk may be. It is the one
// chunk allowed past the 64MB ceiling, and it is not allowed past this.
const TikTokFinalChunkLimit = 128 << 20

// TikTokPhotoMax is how many images one photo post carries.
const TikTokPhotoMax = 35

// TikTok posts through the Content Posting API.
//
// Photos are pulled from a URL; video is pushed in chunks, because
// PULL_FROM_URL for video requires a domain verified with TikTok, which most
// people setting crier up will not have.
type TikTok struct {
	cfg    config.TikTok
	client *httpx.Client
	log    zerolog.Logger
}

func newTikTok(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.TikTok
	if err := require(c.APIBaseURL, "publish.tiktok.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.tiktok.token"); err != nil {
		return nil, err
	}
	switch strings.ToUpper(strings.TrimSpace(c.PrivacyLevel)) {
	case "SELF_ONLY", "MUTUAL_FOLLOW_FRIENDS", "FOLLOWER_OF_CREATOR", "PUBLIC_TO_EVERYONE":
	default:
		return nil, fmt.Errorf("publish.tiktok.privacy-level %q is not one of SELF_ONLY, "+
			"MUTUAL_FOLLOW_FRIENDS, FOLLOWER_OF_CREATOR or PUBLIC_TO_EVERYONE", c.PrivacyLevel)
	}
	return &TikTok{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (t *TikTok) Name() string { return "tiktok" }

// Needs implements Publisher.
//
// A photo post is pulled from a URL, so staging is required; a video is
// uploaded, so it is not. Declaring URL as needed covers the stricter case,
// and a video-only run simply does not use the staged URL.
func (t *TikTok) Needs() Needs {
	return Needs{
		URL:            true,
		Formats:        []config.Format{config.JPEG, config.PNG},
		Kinds:          imageAndVideo,
		MaxAttachments: TikTokPhotoMax,
	}
}

type tiktokInit struct {
	Data struct {
		PublishID string `json:"publish_id"`
		UploadURL string `json:"upload_url"`
	} `json:"data"`
	Error tiktokError `json:"error"`
}

type tiktokError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	LogID   string `json:"log_id"`
}

// ok reports whether the envelope says the call succeeded. TikTok answers 200
// with an error object inside, so the status alone says nothing.
func (e tiktokError) ok() bool {
	return e.Code == "" || strings.EqualFold(e.Code, "ok")
}

func (e tiktokError) err() error {
	return fmt.Errorf("tiktok refused the request: %s: %s (log id %s)", e.Code, e.Message, e.LogID)
}

type tiktokStatus struct {
	Data struct {
		Status                  string  `json:"status"`
		FailReason              string  `json:"fail_reason"`
		PubliclyAvailablePostID []int64 `json:"publicaly_available_post_id"`
	} `json:"data"`
	Error tiktokError `json:"error"`
}

// Publish uploads the artifact and waits for TikTok to finish with it.
func (t *TikTok) Publish(ctx context.Context, in Input) (Result, error) {
	var (
		publishID string
		err       error
	)
	if in.Artifact.Kind == render.KindVideo {
		if err := checkSize(in.Artifact, TikTokVideoLimit, "tiktok"); err != nil {
			return Result{}, err
		}
		publishID, err = t.publishVideo(ctx, in)
	} else {
		publishID, err = t.publishPhoto(ctx, in)
	}
	if err != nil {
		return Result{}, err
	}

	if err := t.await(ctx, publishID); err != nil {
		return Result{}, err
	}
	return Result{ID: publishID, Extra: map[string]string{"publishId": publishID}}, nil
}

func (t *TikTok) postInfo(in Input) map[string]any {
	return map[string]any{
		"title":         firstNonEmpty(t.cfg.Title, in.Caption),
		"description":   firstNonEmpty(in.Caption, t.cfg.Title),
		"privacy_level": strings.ToUpper(strings.TrimSpace(t.cfg.PrivacyLevel)),
	}
}

// publishPhoto starts a photo post, which TikTok pulls from a URL.
func (t *TikTok) publishPhoto(ctx context.Context, in Input) (string, error) {
	if in.URL == "" {
		return "", fmt.Errorf("tiktok needs a public URL for a photo post; configure stage.mode")
	}
	// A photo post is already a list at TikTok, so several pages need no
	// second shape: they are more entries in the one it always sent.
	images := in.SequenceURLs()
	if len(images) > TikTokPhotoMax {
		return "", fmt.Errorf("a tiktok photo post holds %d images and this one has %d",
			TikTokPhotoMax, len(images))
	}
	// auto_add_music is the only music setting any of these APIs offers, and it
	// belongs to a photo post made with DIRECT_POST. TikTok picks the track:
	// nothing anywhere takes the id of one. The video path below does not set
	// it, because TikTok documents it only for a photo post.
	postInfo := t.postInfo(in)
	if t.cfg.AutoAddMusic {
		postInfo["auto_add_music"] = true
		t.log.Debug().Msg("asking tiktok to add a recommended track to the photo post")
	}
	body := map[string]any{
		"post_info": postInfo,
		"source_info": map[string]any{
			"source": "PULL_FROM_URL",
			// The cover is chosen by index rather than by position, and the
			// first page is the one that reads as the cover.
			"photo_cover_index": 0,
			"photo_images":      images,
		},
		"post_mode":  "DIRECT_POST",
		"media_type": "PHOTO",
	}

	var out tiktokInit
	req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "v2/post/publish/content/init/").
		Bearer(t.cfg.Token).
		Header("Content-Type", "application/json; charset=UTF-8").
		JSON(body)
	// init reserves a publish id and starts the post.
	if err := t.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return "", err
	}
	if !out.Error.ok() {
		return "", out.Error.err()
	}
	if out.Data.PublishID == "" {
		return "", fmt.Errorf("tiktok returned no publish id")
	}
	return out.Data.PublishID, nil
}

// publishVideo starts a video post and uploads the file in chunks.
func (t *TikTok) publishVideo(ctx context.Context, in Input) (string, error) {
	chunkSize, chunks := TikTokChunks(in.Artifact.Size)
	if len(chunks) == 0 {
		return "", fmt.Errorf("the video is empty")
	}

	body := map[string]any{
		"post_info": t.postInfo(in),
		"source_info": map[string]any{
			"source":            "FILE_UPLOAD",
			"video_size":        in.Artifact.Size,
			"chunk_size":        chunkSize,
			"total_chunk_count": len(chunks),
		},
	}

	var out tiktokInit
	req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "v2/post/publish/video/init/").
		Bearer(t.cfg.Token).
		Header("Content-Type", "application/json; charset=UTF-8").
		JSON(body)
	if err := t.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return "", err
	}
	if !out.Error.ok() {
		return "", out.Error.err()
	}
	if out.Data.PublishID == "" || out.Data.UploadURL == "" {
		return "", fmt.Errorf("tiktok returned no upload url")
	}

	for _, c := range chunks {
		data, err := readChunk(in.Artifact.Path, c)
		if err != nil {
			return "", err
		}
		upload := httpx.NewRequest(http.MethodPut, out.Data.UploadURL).
			Header("Content-Range", c.ContentRange(in.Artifact.Size)).
			Bytes(in.Artifact.ContentType, data)
		resp, err := t.client.Send(ctx, upload)
		if err != nil {
			return "", fmt.Errorf("uploading chunk %d: %w", c.Index, err)
		}
		_ = resp.Body.Close()
		t.log.Debug().Int("chunk", c.Index).Int64("bytes", c.Size).
			Int("status", resp.StatusCode).Msg("uploaded a video chunk to tiktok")
	}
	return out.Data.PublishID, nil
}

// await polls until TikTok has finished with the upload.
func (t *TikTok) await(ctx context.Context, publishID string) error {
	interval := config.Duration(t.cfg.PollInterval)
	timeout := config.Duration(t.cfg.PollTimeout)
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	err := httpx.Poll(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		var st tiktokStatus
		req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "v2/post/publish/status/fetch/").
			Bearer(t.cfg.Token).
			Header("Content-Type", "application/json; charset=UTF-8").
			JSON(map[string]any{"publish_id": publishID})
		if err := t.client.JSON(ctx, req, &st); err != nil {
			return false, err
		}
		if !st.Error.ok() {
			return false, st.Error.err()
		}
		switch strings.ToUpper(st.Data.Status) {
		case "PUBLISH_COMPLETE", "SEND_TO_USER_INBOX":
			return true, nil
		case "FAILED":
			return false, fmt.Errorf("tiktok could not publish the post: %s", st.Data.FailReason)
		default:
			t.log.Debug().Str("status", st.Data.Status).Msg("waiting for tiktok")
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("waiting for tiktok: %w", err)
	}
	return nil
}

// Ping asks TikTok for the creator the token belongs to.
//
// creator_info/query is the call the Content Posting API expects a client to
// make before every post anyway — it returns the nickname and the privacy
// options the account allows — so it is both the cheapest identity endpoint
// and the one that proves the posting scope is actually granted.
func (t *TikTok) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		Data struct {
			CreatorNickname string `json:"creator_nickname"`
			CreatorUsername string `json:"creator_username"`
		} `json:"data"`
		Error tiktokError `json:"error"`
	}
	req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "v2/post/publish/creator_info/query/").
		Bearer(t.cfg.Token).
		Header("Content-Type", "application/json; charset=UTF-8")
	if err := t.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	if !out.Error.ok() {
		return Identity{}, out.Error.err()
	}
	name := out.Data.CreatorUsername
	if name != "" {
		name = "@" + name
	}
	return Identity{ID: out.Data.CreatorUsername, Name: firstNonEmpty(out.Data.CreatorNickname, name)}, nil
}
