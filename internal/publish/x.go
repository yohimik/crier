package publish

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// XSegmentSize is the largest APPEND segment the media endpoint accepts.
const XSegmentSize = 5 << 20

// XVideoLimit is the largest video X accepts.
const XVideoLimit = 512 << 20

// XGIFLimit is the largest animation X accepts. It is a thirty-fourth of the
// video limit, which is worth checking before uploading rather than after.
const XGIFLimit = 15 << 20

// X posts to X, formerly Twitter.
//
// Images go up in one request; video has to go through the chunked
// INIT/APPEND/FINALIZE dance and then be waited for, because X transcodes
// asynchronously and a tweet referring to a media id that is still processing
// is rejected.
type X struct {
	cfg    config.X
	client *httpx.Client
	log    zerolog.Logger
}

func newX(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.X
	if err := require(c.APIBaseURL, "publish.x.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.x.token"); err != nil {
		return nil, err
	}
	return &X{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (x *X) Name() string { return "x" }

// Needs implements Publisher.
func (x *X) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageVideoAndGIF}
}

type xMediaResponse struct {
	Data struct {
		ID             string `json:"id"`
		MediaKey       string `json:"media_key"`
		ProcessingInfo *struct {
			State          string `json:"state"`
			CheckAfterSecs int    `json:"check_after_secs"`
			ProgressPct    int    `json:"progress_percent"`
			Error          *struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"processing_info"`
	} `json:"data"`
}

type xTweetResponse struct {
	Data struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"data"`
}

// Publish uploads the media and posts a tweet referring to it.
func (x *X) Publish(ctx context.Context, in Input) (Result, error) {
	var (
		mediaID string
		err     error
	)
	switch in.Artifact.Kind {
	case render.KindVideo:
		if err := checkSize(in.Artifact, XVideoLimit, "x"); err != nil {
			return Result{}, err
		}
		mediaID, err = x.uploadChunked(ctx, in.Artifact)
	case render.KindGIF:
		if err := checkSize(in.Artifact, XGIFLimit, "x"); err != nil {
			return Result{}, err
		}
		mediaID, err = x.uploadChunked(ctx, in.Artifact)
	default:
		mediaID, err = x.uploadSimple(ctx, in.Artifact)
	}
	if err != nil {
		return Result{}, err
	}

	body := map[string]any{"text": in.Caption}
	if mediaID != "" {
		body["media"] = map[string]any{"media_ids": []string{mediaID}}
	}

	var tweet xTweetResponse
	req := httpx.NewRequest(http.MethodPost, x.cfg.APIBaseURL, "2/tweets").
		Bearer(x.cfg.Token).
		JSON(body)
	// Posting the tweet is the irreversible step.
	if err := x.client.NoRetry().JSON(ctx, req, &tweet); err != nil {
		return Result{}, err
	}
	return Result{
		ID:    tweet.Data.ID,
		URL:   "https://x.com/i/web/status/" + tweet.Data.ID,
		Extra: map[string]string{"mediaId": mediaID},
	}, nil
}

// uploadSimple posts an image in one request.
func (x *X) uploadSimple(ctx context.Context, a render.Artifact) (string, error) {
	var out xMediaResponse
	req := httpx.NewRequest(http.MethodPost, x.cfg.APIBaseURL, "2/media/upload").
		Bearer(x.cfg.Token).
		Multipart(
			httpx.FilePart("media", a.Path, a.ContentType),
			httpx.Field("media_category", "tweet_image"),
		)
	if err := x.client.JSON(ctx, req, &out); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", fmt.Errorf("x returned no media id")
	}
	return out.Data.ID, nil
}

// uploadChunked runs INIT, APPEND for each segment, FINALIZE, and then waits
// for the transcode to finish.
func (x *X) uploadChunked(ctx context.Context, a render.Artifact) (string, error) {
	id, err := x.initUpload(ctx, a)
	if err != nil {
		return "", err
	}
	for _, c := range SplitChunks(a.Size, XSegmentSize) {
		data, err := readChunk(a.Path, c)
		if err != nil {
			return "", err
		}
		req := httpx.NewRequest(http.MethodPost, x.cfg.APIBaseURL, "2/media/upload").
			Bearer(x.cfg.Token).
			Multipart(
				httpx.Field("command", "APPEND"),
				httpx.Field("media_id", id),
				httpx.Field("segment_index", strconv.Itoa(c.Index)),
				httpx.BytesPart("media", "chunk", "application/octet-stream", data),
			)
		if err := x.client.Discard(ctx, req); err != nil {
			return "", fmt.Errorf("uploading segment %d: %w", c.Index, err)
		}
		x.log.Debug().Int("segment", c.Index).Int64("bytes", c.Size).Msg("uploaded a video segment to x")
	}

	var fin xMediaResponse
	req := httpx.NewRequest(http.MethodPost, x.cfg.APIBaseURL, "2/media/upload").
		Bearer(x.cfg.Token).
		Form(url.Values{"command": {"FINALIZE"}, "media_id": {id}})
	if err := x.client.JSON(ctx, req, &fin); err != nil {
		return "", fmt.Errorf("finalizing the upload: %w", err)
	}
	if err := x.awaitProcessing(ctx, id, fin); err != nil {
		return "", err
	}
	return id, nil
}

// xCategory is what X calls the thing being uploaded. It decides how the
// media is transcoded and where it may be attached, so a GIF sent as
// tweet_video comes out as a silent video rather than an animation.
func xCategory(kind render.Kind) string {
	if kind == render.KindGIF {
		return "tweet_gif"
	}
	return "tweet_video"
}

func (x *X) initUpload(ctx context.Context, a render.Artifact) (string, error) {
	var out xMediaResponse
	req := httpx.NewRequest(http.MethodPost, x.cfg.APIBaseURL, "2/media/upload").
		Bearer(x.cfg.Token).
		Form(url.Values{
			"command":        {"INIT"},
			"total_bytes":    {strconv.FormatInt(a.Size, 10)},
			"media_type":     {a.ContentType},
			"media_category": {xCategory(a.Kind)},
		})
	// INIT reserves a media id; repeating it would leak one, so it is not
	// retried beyond a rate limit.
	if err := x.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return "", fmt.Errorf("starting the upload: %w", err)
	}
	if out.Data.ID == "" {
		return "", fmt.Errorf("x returned no media id")
	}
	return out.Data.ID, nil
}

// awaitProcessing polls STATUS until X says the media is ready.
//
// X tells us how long to wait in check_after_secs, and honouring it is the
// difference between one poll and a dozen 429s.
func (x *X) awaitProcessing(ctx context.Context, id string, fin xMediaResponse) error {
	info := fin.Data.ProcessingInfo
	if info == nil || info.State == "succeeded" {
		return nil
	}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		wait := time.Duration(info.CheckAfterSecs) * time.Second
		if wait <= 0 {
			wait = time.Second
		}
		if time.Now().Add(wait).After(deadline) {
			return fmt.Errorf("x was still processing the video after 10 minutes")
		}
		if err := httpx.Sleep(ctx, wait); err != nil {
			return err
		}

		var out xMediaResponse
		req := httpx.NewRequest(http.MethodGet, x.cfg.APIBaseURL, "2/media/upload").
			Bearer(x.cfg.Token).
			Query("command", "STATUS").
			Query("media_id", id)
		if err := x.client.JSON(ctx, req, &out); err != nil {
			return fmt.Errorf("checking the upload status: %w", err)
		}
		info = out.Data.ProcessingInfo
		if info == nil || info.State == "succeeded" {
			return nil
		}
		if info.State == "failed" {
			if info.Error != nil {
				return fmt.Errorf("x could not process the video: %s: %s", info.Error.Name, info.Error.Message)
			}
			return fmt.Errorf("x could not process the video")
		}
		x.log.Debug().Str("state", info.State).Int("percent", info.ProgressPct).Msg("x is processing the video")
	}
}

// Ping reads the account the token posts as.
func (x *X) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"data"`
	}
	req := httpx.NewRequest(http.MethodGet, x.cfg.APIBaseURL, "2/users/me").Bearer(x.cfg.Token)
	if err := x.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	name := out.Data.Username
	if name != "" {
		name = "@" + name
	}
	return Identity{ID: out.Data.ID, Name: firstNonEmpty(name, out.Data.Name)}, nil
}
