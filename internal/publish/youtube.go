package publish

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// YouTubeTitleMax is how long a video title may be.
//
// A hundred characters, counted by the API rather than by anything a template
// knows about, and a title over it is refused outright. crier truncates instead:
// the title is a line lifted out of the caption, and losing the tail of it is
// better than losing the upload.
const YouTubeTitleMax = 100

// YouTubeAttachmentMax is how many files one upload carries. One, and there is
// no arrangement of calls that makes it two: a video is a video.
const YouTubeAttachmentMax = 1

// YouTube uploads to a channel through the YouTube Data API v3.
//
// It is the one platform here that takes no pictures. The Data API uploads
// videos and nothing else — a community post, which is where a still image
// would go, has no public API at all — so Needs declares video and lets the
// pipeline refuse an image-only run before anything is rendered.
//
// Two hosts, the way Reddit has two: the token comes from Google's OAuth host
// and everything else goes to the API host. Both are configurable, which is
// what lets the end-to-end tests point them at one fake.
//
// The upload itself is resumable in two steps. An initiate call carries the
// video's metadata as JSON and answers with an upload session URL in its
// Location header; the bytes are then PUT to that URL, streamed from disk.
type YouTube struct {
	cfg    config.YouTube
	client *httpx.Client
	log    zerolog.Logger

	// once and token hold the access token for the run. Refreshing is one call
	// per process rather than one per request: the token lasts an hour and a
	// publish and a thumbnail are seconds apart.
	once  sync.Once
	token string
	err   error
}

func newYouTube(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.YouTube
	if err := require(c.APIBaseURL, "publish.youtube.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.AuthBaseURL, "publish.youtube.auth-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.ClientID, "publish.youtube.client-id"); err != nil {
		return nil, err
	}
	if err := require(c.ClientSecret, "publish.youtube.client-secret"); err != nil {
		return nil, err
	}
	if err := require(c.RefreshToken, "publish.youtube.refresh-token"); err != nil {
		return nil, err
	}
	if err := checkYouTubePrivacy(c.PrivacyStatus); err != nil {
		return nil, err
	}
	if c.MaxAttachments > YouTubeAttachmentMax {
		return nil, fmt.Errorf(
			"publish.youtube.max-attachments is %d and a youtube upload is %d video; "+
				"there is no carousel here to lower",
			c.MaxAttachments, YouTubeAttachmentMax)
	}
	return &YouTube{cfg: c, client: d.Client, log: d.Logger}, nil
}

// checkYouTubePrivacy refuses a privacy status YouTube does not have, by name.
func checkYouTubePrivacy(status string) error {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "private", "unlisted", "public":
		return nil
	default:
		return fmt.Errorf("publish.youtube.privacy-status %q is not one of private, unlisted or public",
			status)
	}
}

// Name implements Publisher.
func (y *YouTube) Name() string { return "youtube" }

// Needs implements Publisher.
//
// Video, and only video. No image format is listed because no image is ever
// asked for: the Data API's videos.insert is the whole of what it can post, and
// there is no community-posts endpoint to put a picture through. An image-only
// run with youtube enabled is refused by the pipeline, naming the platform,
// before anything is staged.
//
// An animation is not a kind either. A GIF uploaded as a video would be a
// silent clip rather than an animation, which is the sort of quiet substitution
// the kind gate exists to prevent.
//
// The bytes are pushed, so no URL is needed. One file per upload.
func (y *YouTube) Needs() Needs {
	return Needs{
		Kinds:          []render.Kind{render.KindVideo},
		MaxAttachments: YouTubeAttachmentMax,
	}
}

// youtubeToken is what the OAuth token endpoint answers with.
type youtubeToken struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// youtubeVideo is the video resource the upload answers with.
type youtubeVideo struct {
	ID      string `json:"id"`
	Snippet struct {
		Title string `json:"title"`
	} `json:"snippet"`
}

// Publish uploads the clip and reports where it landed.
func (y *YouTube) Publish(ctx context.Context, in Input) (Result, error) {
	if in.Artifact.Kind != render.KindVideo {
		return Result{}, fmt.Errorf("youtube uploads videos and this post is %s; "+
			"there is no public API for a community post", in.Artifact.Kind)
	}

	token, err := y.accessToken(ctx)
	if err != nil {
		return Result{}, err
	}

	session, err := y.initiate(ctx, token, in)
	if err != nil {
		return Result{}, err
	}

	video, err := y.upload(ctx, token, session, in.Artifact)
	if err != nil {
		return Result{}, err
	}
	if video.ID == "" {
		return Result{}, fmt.Errorf("youtube accepted the upload and named no video id")
	}

	res := Result{
		ID:    video.ID,
		URL:   "https://www.youtube.com/watch?v=" + video.ID,
		Extra: map[string]string{"videoId": video.ID},
	}
	if thumb := strings.TrimSpace(y.cfg.Thumbnail); thumb != "" {
		if err := y.thumbnail(ctx, token, video.ID, thumb); err != nil {
			// Not a failure of the post. The video is up; a thumbnail needs a
			// phone-verified account, and Google answers 403 when the account is
			// not one. Turning that into a failed run would say the upload did
			// not happen, which is the opposite of what happened.
			y.log.Warn().Str("video", video.ID).Str("thumbnail", thumb).Err(err).
				Msg("the video was uploaded but the custom thumbnail was refused")
			res.Extra["thumbnailError"] = err.Error()
		} else {
			res.Extra["thumbnail"] = thumb
		}
	}
	return res, nil
}

// accessToken refreshes the token once and hands the same one out afterwards.
func (y *YouTube) accessToken(ctx context.Context) (string, error) {
	y.once.Do(func() { y.token, y.err = y.refresh(ctx) })
	return y.token, y.err
}

// refresh trades the refresh token for an access token.
//
// The call creates nothing, so it is retryable like any other read. A 400 or a
// 401 from it is a credential problem rather than a transient one, and the
// message says which of the three values to look at, because Google's own
// answer is "invalid_grant" and nothing else.
func (y *YouTube) refresh(ctx context.Context) (string, error) {
	form := url.Values{
		"client_id":     {y.cfg.ClientID},
		"client_secret": {y.cfg.ClientSecret},
		"refresh_token": {y.cfg.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	var out youtubeToken
	req := httpx.NewRequest(http.MethodPost, y.cfg.AuthBaseURL, "token").Form(form)
	if err := y.client.JSON(ctx, req, &out); err != nil {
		return "", fmt.Errorf("refreshing the youtube access token: %w "+
			"(check publish.youtube.client-id, client-secret and refresh-token; "+
			"a refresh token is revoked when the account's password changes)", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("refreshing the youtube access token: %s: %s",
			out.Error, firstNonEmpty(out.ErrorDescription, "no reason given"))
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("google returned no access token")
	}
	y.log.Debug().Int("expires_in", out.ExpiresIn).Msg("refreshed the youtube access token")
	return out.AccessToken, nil
}

// initiate creates the upload session and returns the URL the bytes go to.
//
// The metadata travels here rather than with the file: snippet is the title,
// description and category, and status is the privacy setting. The session URL
// comes back in the Location header.
func (y *YouTube) initiate(ctx context.Context, token string, in Input) (string, error) {
	body := map[string]any{
		"snippet": map[string]any{
			"title":       y.title(in),
			"description": in.Caption,
			"categoryId":  firstNonEmpty(y.cfg.CategoryID, "22"),
		},
		"status": map[string]any{
			"privacyStatus": strings.ToLower(strings.TrimSpace(y.cfg.PrivacyStatus)),
			// Declared rather than left out: the API requires an answer, and
			// "not made for kids" is the one crier can honestly give for a
			// release announcement. A channel that needs the other answer sets
			// it on the video afterwards.
			"selfDeclaredMadeForKids": false,
		},
	}

	req := httpx.NewRequest(http.MethodPost, y.cfg.APIBaseURL, "upload/youtube/v3/videos").
		Query("uploadType", "resumable").
		Query("part", "snippet,status").
		Bearer(token).
		JSON(body)
	// A resumable session is state on Google's side. Asking twice would leave a
	// session behind, and the second one is the only one that gets used.
	resp, err := y.client.NoRetry().Send(ctx, req)
	if err != nil {
		return "", fmt.Errorf("starting the youtube upload: %w", err)
	}
	session := resp.Header.Get("Location")
	_ = resp.Body.Close()
	if session == "" {
		return "", fmt.Errorf("youtube started an upload session and named no Location to send it to")
	}
	y.log.Debug().Str("session", httpx.RedactURLString(session)).Msg("opened a youtube upload session")
	return session, nil
}

// upload sends the bytes to the session URL, streamed from disk.
//
// It is never retried. The session URL is single-use state, and a PUT that was
// received and whose answer was lost would become a second video if it were
// sent again. Resuming a half-finished upload — the protocol's own answer, a
// Content-Range probe followed by the remaining bytes — is a follow-up rather
// than something v1 needs: crier's clips are seconds long, and a failed upload
// is cheaper to repeat than to resume.
func (y *YouTube) upload(ctx context.Context, token, session string, a render.Artifact) (youtubeVideo, error) {
	var out youtubeVideo
	req := httpx.NewRequest(http.MethodPut, session).
		Bearer(token).
		File(firstNonEmpty(a.ContentType, render.VideoContentType), a.Path)
	if err := y.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return youtubeVideo{}, fmt.Errorf("uploading the video: %w", err)
	}
	y.log.Debug().Str("video", out.ID).Int64("bytes", a.Size).Msg("uploaded a video to youtube")
	return out, nil
}

// thumbnail sets a custom thumbnail on a video that is already up.
//
// Its failure is the caller's warning rather than an error: see Publish.
func (y *YouTube) thumbnail(ctx context.Context, token, videoID, path string) error {
	contentType := youtubeImageType(path)
	if contentType == "" {
		return fmt.Errorf("%s is not a JPEG or a PNG; youtube takes one of those as a thumbnail", path)
	}
	req := httpx.NewRequest(http.MethodPost, y.cfg.APIBaseURL, "upload/youtube/v3/thumbnails/set").
		Query("videoId", videoID).
		Query("uploadType", "media").
		Bearer(token).
		File(contentType, path)
	// Setting a thumbnail replaces whatever is there, so repeating it is safe.
	if err := y.client.Discard(ctx, req); err != nil {
		return fmt.Errorf("setting the thumbnail (it needs a phone-verified channel): %w", err)
	}
	y.log.Debug().Str("video", videoID).Str("thumbnail", path).Msg("set a youtube thumbnail")
	return nil
}

// youtubeImageType names a thumbnail's MIME type from its extension, and is
// empty for anything YouTube will not take.
func youtubeImageType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	default:
		return ""
	}
}

// title is the video's title.
//
// The chain is: the configured title, then the caption's first line cut to a
// hundred characters, then "crier". A title is mandatory at the API and an
// empty one is refused, so the last step is a name rather than a blank.
func (y *YouTube) title(in Input) string {
	if t := youtubeTitle(y.cfg.Title); t != "" {
		return t
	}
	first := in.Caption
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if t := youtubeTitle(first); t != "" {
		if len([]rune(strings.TrimSpace(first))) > YouTubeTitleMax {
			y.log.Debug().Int("limit", YouTubeTitleMax).
				Msg("the youtube title was cut to fit; set publish.youtube.title to choose one")
		}
		return t
	}
	return "crier"
}

// youtubeTitle makes one string into a title YouTube will accept: the angle
// brackets removed, and the whole cut to the length limit.
//
// The brackets are not escaped or replaced with anything. YouTube refuses a
// title containing "<" or ">" outright, whatever they are meant to be, so a
// caption written with a "<v2>" in it would otherwise fail the upload for a
// reason nothing in the message explains.
func youtubeTitle(s string) string {
	out := strings.TrimSpace(s)
	if strings.ContainsAny(out, "<>") {
		out = strings.NewReplacer("<", "", ">", "").Replace(out)
		out = strings.TrimSpace(out)
	}
	if r := []rune(out); len(r) > YouTubeTitleMax {
		out = strings.TrimSpace(string(r[:YouTubeTitleMax]))
	}
	return out
}

// Ping refreshes the token and reads the channel it belongs to.
//
// The refresh is most of the check: a client id from the wrong project, a
// secret that does not match it, or a refresh token revoked by a password
// change all fail there. The channels call then confirms the token reaches the
// API and names the channel a video would land on.
func (y *YouTube) Ping(ctx context.Context) (Identity, error) {
	token, err := y.accessToken(ctx)
	if err != nil {
		return Identity{}, err
	}
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		} `json:"items"`
	}
	req := httpx.NewRequest(http.MethodGet, y.cfg.APIBaseURL, "youtube/v3/channels").
		Query("part", "snippet").
		Query("mine", "true").
		Bearer(token)
	if err := y.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	if len(out.Items) == 0 {
		// A Google account is not a channel. The token is good and there is
		// nowhere for a video to go, which is a state worth naming: it is the
		// commonest way a YouTube setup passes every credential check and then
		// fails at the upload.
		return Identity{Note: "the token works and the account has no youtube channel; " +
			"create one at youtube.com and authorise again"}, nil
	}
	ch := out.Items[0]
	return Identity{ID: ch.ID, Name: ch.Snippet.Title}, nil
}
