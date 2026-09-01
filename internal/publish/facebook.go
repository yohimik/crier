package publish

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// Facebook posts to a Page.
//
// Unlike Instagram it will take the bytes, so it works with no staging at all.
// A Page story is two calls rather than one: the photo is uploaded unpublished
// and then turned into a story.
type Facebook struct {
	cfg    config.Facebook
	client *httpx.Client
	log    zerolog.Logger
}

func newFacebook(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Facebook
	if err := require(c.APIBaseURL, "publish.facebook.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.facebook.token"); err != nil {
		return nil, err
	}
	if err := require(c.PageID, "publish.facebook.page-id"); err != nil {
		return nil, err
	}
	return &Facebook{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (f *Facebook) Name() string { return "facebook" }

// Needs implements Publisher.
func (f *Facebook) Needs() Needs {
	return Needs{URL: f.cfg.UseURL, Formats: imageFormats, Kinds: imageAndVideo}
}

type fbUpload struct {
	ID     string `json:"id"`
	PostID string `json:"post_id"`
}

type fbStory struct {
	PostID  string `json:"post_id"`
	Success bool   `json:"success"`
}

// Publish posts the artifact to the Page.
func (f *Facebook) Publish(ctx context.Context, in Input) (Result, error) {
	if in.Artifact.Kind == render.KindVideo {
		return f.publishVideo(ctx, in)
	}
	return f.publishPhoto(ctx, in)
}

func (f *Facebook) publishPhoto(ctx context.Context, in Input) (Result, error) {
	upload, err := f.uploadPhoto(ctx, in, f.cfg.Story)
	if err != nil {
		return Result{}, err
	}
	if !f.cfg.Story {
		id := firstNonEmpty(upload.PostID, upload.ID)
		return Result{ID: id, URL: "https://www.facebook.com/" + id, Extra: map[string]string{"photoId": upload.ID}}, nil
	}

	var story fbStory
	req := httpx.NewRequest(http.MethodPost, f.cfg.APIBaseURL, f.cfg.PageID, "photo_stories").
		Form(url.Values{"photo_id": {upload.ID}, "access_token": {f.cfg.Token}})
	if err := f.client.NoRetry().JSON(ctx, req, &story); err != nil {
		return Result{}, fmt.Errorf("turning the photo into a story: %w", err)
	}
	return Result{
		ID:    firstNonEmpty(story.PostID, upload.ID),
		Extra: map[string]string{"photoId": upload.ID},
	}, nil
}

// uploadPhoto posts the image, published or not.
func (f *Facebook) uploadPhoto(ctx context.Context, in Input, unpublished bool) (fbUpload, error) {
	var out fbUpload
	req := httpx.NewRequest(http.MethodPost, f.cfg.APIBaseURL, f.cfg.PageID, "photos")

	if f.cfg.UseURL {
		if in.URL == "" {
			return out, fmt.Errorf("publish.facebook.use-url is set but nothing staged a URL")
		}
		form := url.Values{"url": {in.URL}, "access_token": {f.cfg.Token}}
		if in.Caption != "" && !unpublished {
			form.Set("message", in.Caption)
		}
		if unpublished {
			form.Set("published", "false")
		}
		req = req.Form(form)
	} else {
		parts := []httpx.Part{
			httpx.Field("access_token", f.cfg.Token),
			httpx.FilePart("source", in.Artifact.Path, in.Artifact.ContentType),
		}
		if in.Caption != "" && !unpublished {
			parts = append(parts, httpx.Field("message", in.Caption))
		}
		if unpublished {
			parts = append(parts, httpx.Field("published", "false"))
		}
		req = req.Multipart(parts...)
	}

	client := f.client
	if !unpublished {
		// A published photo is the post itself.
		client = client.NoRetry()
	}
	if err := client.JSON(ctx, req, &out); err != nil {
		return out, fmt.Errorf("uploading the photo: %w", err)
	}
	if out.ID == "" {
		return out, fmt.Errorf("facebook returned no photo id")
	}
	return out, nil
}

// publishVideo posts a video to the Page's video endpoint.
func (f *Facebook) publishVideo(ctx context.Context, in Input) (Result, error) {
	var out fbUpload
	req := httpx.NewRequest(http.MethodPost, f.cfg.APIBaseURL, f.cfg.PageID, "videos")

	if f.cfg.UseURL {
		if in.URL == "" {
			return Result{}, fmt.Errorf("publish.facebook.use-url is set but nothing staged a URL")
		}
		form := url.Values{"file_url": {in.URL}, "access_token": {f.cfg.Token}}
		if in.Caption != "" {
			form.Set("description", in.Caption)
		}
		req = req.Form(form)
	} else {
		parts := []httpx.Part{
			httpx.Field("access_token", f.cfg.Token),
			httpx.FilePart("source", in.Artifact.Path, in.Artifact.ContentType),
		}
		if in.Caption != "" {
			parts = append(parts, httpx.Field("description", in.Caption))
		}
		req = req.Multipart(parts...)
	}

	if err := f.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return Result{}, fmt.Errorf("uploading the video: %w", err)
	}
	id := firstNonEmpty(out.PostID, out.ID)
	return Result{ID: id, URL: "https://www.facebook.com/" + id, Extra: map[string]string{"videoId": out.ID}}, nil
}
