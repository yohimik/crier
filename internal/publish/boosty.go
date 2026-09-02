package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// Boosty posts to a blog on boosty.to.
//
// There is no official API. What crier talks to is the same undocumented REST
// service the Boosty web app itself uses, the one every community client binds,
// and it can change without notice. That is why both hosts are configuration
// keys rather than constants: when the shape moves, a base URL can be pointed
// somewhere else without a new release, and the end-to-end fake pins the
// contract crier expects today.
//
// A post is made in two halves. Every picture goes to the upload host in three
// calls — a slot, the bytes, a completion — and comes back as a file id. Then
// one call to the API host creates the post, carrying those ids inside a
// content-block array that is JSON encoded into a form field. So a paginated
// run is one post with several image blocks rather than several posts.
//
// The access level is not a separate endpoint. Two fields on that same call say
// it: a price opens the post to a one-time purchase, and a subscription level
// id limits it to a tier. Both zero is a post everybody can read.
type Boosty struct {
	cfg    config.Boosty
	client *httpx.Client
	log    zerolog.Logger

	// mu guards the token pair, which a refresh replaces mid-run.
	mu sync.Mutex
	// token is the access token calls carry now: the configured one until a
	// refresh replaces it.
	token string
	// refreshed records that a refresh has already been attempted, so a token
	// that is simply wrong is traded once rather than on every call.
	refreshed bool
}

// The three access levels a Boosty post can have.
const (
	// BoostyAccessFree is a post everybody can read.
	BoostyAccessFree = "free"
	// BoostyAccessPaid is a post unlocked by a one-time purchase.
	BoostyAccessPaid = "paid"
	// BoostyAccessLevel is a post limited to a subscription tier.
	BoostyAccessLevel = "level"
)

// BoostyAttachmentMax is how many pictures crier puts in one post.
//
// Boosty documents nothing, and no community client names a limit either, so
// this is crier's own ceiling rather than the platform's. Ten is the number
// every other album-shaped platform here settled on, and a longer page list
// becomes several posts in a row the way it does everywhere else.
const BoostyAttachmentMax = 10

// BoostyChunkSize is how much of a file goes up in one part.
//
// Five megabytes, which is what the web editor uses. Parts are numbered from
// one in an X-PartNumber header, and a part sent without that header is
// refused, so the number is not decoration.
const BoostyChunkSize = 5 << 20

// boostyImagesCDN is where an uploaded picture is served from.
//
// It is part of the create-post protocol rather than an address anybody
// configures: the image block a post carries has to name a URL, and this is
// the host the editor writes into it. It is deliberately not a configuration
// key, because pointing it elsewhere would produce a post whose pictures are
// hosted nowhere.
const boostyImagesCDN = "https://images.boosty.to"

func newBoosty(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Boosty
	if err := require(c.APIBaseURL, "publish.boosty.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.UploadBaseURL, "publish.boosty.upload-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Blog, "publish.boosty.blog"); err != nil {
		return nil, err
	}
	if err := require(c.AccessToken, "publish.boosty.access-token"); err != nil {
		return nil, err
	}
	if err := checkBoostyAccess(c); err != nil {
		return nil, err
	}
	// Both halves of the refresh, or neither. One on its own is a refresh that
	// can never run, and the run would only find that out at the first 401 —
	// which is hours later, on a token that has already expired.
	if (strings.TrimSpace(c.RefreshToken) == "") != (strings.TrimSpace(c.DeviceID) == "") {
		set, unset := "publish.boosty.refresh-token", "publish.boosty.device-id"
		if strings.TrimSpace(c.DeviceID) != "" {
			set, unset = unset, set
		}
		return nil, fmt.Errorf(
			"%s is set and %s is not; a boosty refresh needs both, and one on its own could never run",
			set, unset)
	}
	return &Boosty{cfg: c, client: d.Client, log: d.Logger, token: c.AccessToken}, nil
}

// checkBoostyAccess refuses an access level Boosty has no field for, and an
// access level whose own key is missing.
func checkBoostyAccess(c config.Boosty) error {
	switch boostyAccess(c.Access) {
	case BoostyAccessFree:
		return nil
	case BoostyAccessPaid:
		if c.Price <= 0 {
			return fmt.Errorf(
				"publish.boosty.access is paid, so publish.boosty.price has to be more than 0 "+
					"(it is the one-time price in %s)", boostyCurrency(c.Currency))
		}
		return nil
	case BoostyAccessLevel:
		return require(c.LevelID, "publish.boosty.level-id")
	default:
		return fmt.Errorf("publish.boosty.access %q is not one of free, paid or level", c.Access)
	}
}

// boostyAccess normalises the access level as written.
func boostyAccess(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// boostyCurrency is the currency a price is quoted in, defaulting to the one
// the web app sends.
func boostyCurrency(s string) string {
	if v := strings.ToUpper(strings.TrimSpace(s)); v != "" {
		return v
	}
	return "RUB"
}

// Name implements Publisher.
func (b *Boosty) Name() string { return "boosty" }

// Needs implements Publisher.
//
// Pictures, and the bytes rather than a URL. Video is out: the upload host has
// an endpoint per media kind and only the audio and image ones are pinned by
// anything crier could read, and a clip that went up through a guessed endpoint
// would sit in a post as a file nobody can play. An animation is not a kind
// here either, for the same reason — nothing says a GIF survives the image
// pipeline as an animation rather than as a still.
func (b *Boosty) Needs() Needs {
	return Needs{
		Formats:        imageFormats,
		Kinds:          imageOnly,
		MaxAttachments: BoostyAttachmentMax,
	}
}

// boostyText is a text block of a post's content array.
//
// The content field is itself a JSON string — a three element array of the
// text, a paragraph style and a list of entities — which is the editor's own
// draft-style serialisation. A logical paragraph is closed by a second block
// carrying no content and the BLOCK_END marker.
type boostyText struct {
	Type        string `json:"type"`
	Content     string `json:"content"`
	Modificator string `json:"modificator"`
}

// boostyImage is a picture block of a post's content array.
//
// The id and the uploadId are both the file id the upload host handed back;
// the editor sends both. An empty rendition is the full-size picture, as
// opposed to the cropped one a teaser uses.
type boostyImage struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	UploadID  string         `json:"uploadId"`
	URL       string         `json:"url"`
	Rendition string         `json:"rendition"`
	Size      int64          `json:"size"`
	Data      map[string]any `json:"data"`
}

// boostyPost is what the create call answers with.
type boostyPost struct {
	ID    string `json:"id"`
	IntID int64  `json:"int_id"`
	Title string `json:"title"`
}

// Publish uploads every page and makes one post of them.
func (b *Boosty) Publish(ctx context.Context, in Input) (Result, error) {
	arts := in.Sequence()
	if len(arts) > BoostyAttachmentMax {
		return Result{}, fmt.Errorf("crier puts %d pictures in one boosty post and this post has %d",
			BoostyAttachmentMax, len(arts))
	}

	blocks := boostyTextBlocks(in.Caption)
	for n, a := range arts {
		id, err := b.upload(ctx, a)
		if err != nil {
			if len(arts) > 1 {
				return Result{}, fmt.Errorf("picture %d of %d: %w", n+1, len(arts), err)
			}
			return Result{}, err
		}
		blocks = append(blocks, boostyImage{
			Type:      "image",
			ID:        id,
			UploadID:  id,
			URL:       boostyImagesCDN + "/image/" + id,
			Rendition: "",
			Size:      a.Size,
			Data:      map[string]any{},
		})
	}

	data, err := json.Marshal(blocks)
	if err != nil {
		return Result{}, fmt.Errorf("encoding the boosty post content: %w", err)
	}

	price, level := b.accessFields()
	form := url.Values{
		"title": {b.title(in)},
		"data":  {string(data)},
		// A teaser is what a reader without access sees. crier sends none: the
		// pictures are the post, and a teaser assembled out of them would give
		// away the thing a paid post is charging for.
		"teaser_data":           {"[]"},
		"price":                 {price},
		"subscription_level_id": {level},
		"tags":                  {""},
		"deny_comments":         {"false"},
		"wait_video":            {"false"},
		"has_chat":              {"false"},
		"advertiser_info":       {""},
	}

	var post boostyPost
	// Creating the post is the act itself. A 5xx may mean it was created and
	// the answer was lost, and asking again would put the same cards up twice.
	err = b.do(ctx, b.client.NoRetry(), func(token string) *httpx.Builder {
		return b.request(http.MethodPost, b.cfg.APIBaseURL, token, "v1", "blog", b.cfg.Blog, "post/").
			Form(form)
	}, &post)
	if err != nil {
		return Result{}, fmt.Errorf("creating the boosty post: %w", err)
	}
	if post.ID == "" {
		return Result{}, fmt.Errorf("boosty accepted the post and named no post id")
	}

	b.log.Debug().Str("post", post.ID).Int("pictures", len(arts)).
		Str("access", boostyAccess(b.cfg.Access)).Msg("posted to boosty")
	return Result{
		ID:  post.ID,
		URL: "https://boosty.to/" + b.cfg.Blog + "/posts/" + post.ID,
		Extra: map[string]string{
			"access":   boostyAccess(b.cfg.Access),
			"pictures": strconv.Itoa(len(arts)),
		},
	}, nil
}

// accessFields turns the configured access level into the pair of form fields
// that carry it.
//
// There is no "access" field at the API. A price above zero is a one-time
// purchase, a subscription level id is a tier, and two zeroes are a post
// anybody can read — which is why the three levels are one configuration key
// here and two numbers on the wire.
func (b *Boosty) accessFields() (price, level string) {
	switch boostyAccess(b.cfg.Access) {
	case BoostyAccessPaid:
		return strconv.Itoa(b.cfg.Price), "0"
	case BoostyAccessLevel:
		return "0", strings.TrimSpace(b.cfg.LevelID)
	default:
		return "0", "0"
	}
}

// boostyTextBlocks turns the caption into the content blocks that carry it.
//
// One block per paragraph, each closed by its own BLOCK_END, which is how the
// editor writes a multi-line post. A post with no text at all still gets one
// empty paragraph: Boosty refuses a post whose content array is empty, and a
// run of pictures with no caption is an ordinary thing to want.
func boostyTextBlocks(caption string) []any {
	var out []any
	for _, line := range strings.Split(caption, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, boostyText{Type: "text", Content: boostyInline(line), Modificator: ""})
		out = append(out, boostyText{Type: "text", Content: "", Modificator: "BLOCK_END"})
	}
	if len(out) == 0 {
		out = append(out,
			boostyText{Type: "text", Content: boostyInline(""), Modificator: ""},
			boostyText{Type: "text", Content: "", Modificator: "BLOCK_END"})
	}
	return out
}

// boostyInline is the JSON string a text block's content field holds: the
// paragraph, its style, and the entities it has none of.
func boostyInline(text string) string {
	inner, err := json.Marshal([]any{text, "unstyled", []any{}})
	if err != nil {
		// Marshalling a string, a string and an empty slice cannot fail. The
		// branch exists so the caller is not handed an error it could do
		// nothing with.
		return `["","unstyled",[]]`
	}
	return string(inner)
}

// title is the post's title.
//
// The chain is the configured title, then the caption's first line, then
// crier's own name. Boosty shows the title above the post and an untitled post
// reads as a mistake, so the last step is a name rather than a blank.
func (b *Boosty) title(in Input) string {
	if t := strings.TrimSpace(b.cfg.Title); t != "" {
		return t
	}
	first := in.Caption
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if t := strings.TrimSpace(first); t != "" {
		return t
	}
	return "crier"
}

// boostyUpload is what the upload host answers a slot request with.
type boostyUpload struct {
	FileID string `json:"fileId"`
}

// upload puts one picture on the upload host and returns its file id.
//
// Three calls, which is what the web editor makes: a slot, the bytes, and a
// completion. The bytes go up in numbered parts because that is the only shape
// the host takes — a part without an X-PartNumber header is refused — and a
// card small enough to be one part is still part one.
func (b *Boosty) upload(ctx context.Context, a render.Artifact) (string, error) {
	var slot boostyUpload
	err := b.do(ctx, b.client, func(token string) *httpx.Builder {
		// An empty object, not an empty body: the image endpoint takes the
		// same JSON shape the audio one does, with nothing to say in it.
		return b.request(http.MethodPost, b.cfg.UploadBaseURL, token, "image").JSON(map[string]any{})
	}, &slot)
	if err != nil {
		return "", fmt.Errorf("asking boosty for an upload slot: %w", err)
	}
	if slot.FileID == "" {
		return "", fmt.Errorf("boosty opened an upload and named no file id")
	}

	chunks := SplitChunks(a.Size, BoostyChunkSize)
	if len(chunks) == 0 {
		return "", fmt.Errorf("%s is empty; boosty takes no empty picture", a.Path)
	}
	for _, c := range chunks {
		body, err := readChunk(a.Path, c)
		if err != nil {
			return "", err
		}
		part := strconv.Itoa(c.Index + 1)
		// A part is addressed by its file id and its number, so sending it
		// again replaces the same part rather than appending a second one.
		// That makes it an ordinary retryable write.
		err = b.do(ctx, b.client, func(token string) *httpx.Builder {
			return b.request(http.MethodPost, b.cfg.UploadBaseURL, token, "upload", slot.FileID).
				Header("X-PartNumber", part).
				Bytes("application/octet-stream", body)
		}, nil)
		if err != nil {
			return "", fmt.Errorf("uploading part %s of %s to boosty: %w", part, a.Path, err)
		}
	}

	// Completion is the one upload call that is not a repeatable write: it
	// finishes the file, and a second one has nothing left to finish.
	err = b.do(ctx, b.client.NoRetry(), func(token string) *httpx.Builder {
		return b.request(http.MethodPost, b.cfg.UploadBaseURL, token, "upload", slot.FileID, "complete")
	}, nil)
	if err != nil {
		return "", fmt.Errorf("finishing the boosty upload of %s: %w", a.Path, err)
	}

	b.log.Debug().Str("file", slot.FileID).Int("parts", len(chunks)).Int64("bytes", a.Size).
		Msg("uploaded a picture to boosty")
	return slot.FileID, nil
}

// request starts a call to either host with the headers Boosty expects.
//
// The X- headers are what the web app sends on every call. X-Currency is the
// one that carries meaning rather than provenance: it says which currency a
// price is quoted in, so it decides what publish.boosty.price means.
func (b *Boosty) request(method, base, token string, segments ...string) *httpx.Builder {
	return httpx.NewRequest(method, base, segments...).
		Bearer(token).
		Header("X-App", "web").
		Header("X-Currency", boostyCurrency(b.cfg.Currency))
}

// do sends a call, and refreshes the token once if the call says the token is
// no longer good.
//
// The builder is a function rather than a value because a retry has to build
// the request again with the new token. Refreshing happens at most once per
// run: a token that is simply wrong would otherwise be traded for another
// wrong one on every call.
func (b *Boosty) do(ctx context.Context, client *httpx.Client, build func(token string) *httpx.Builder, out any) error {
	err := client.JSON(ctx, build(b.bearer()), out)
	if err == nil || !boostyUnauthorized(err) {
		return err
	}

	token, renewed, rerr := b.renew(ctx)
	switch {
	case rerr != nil:
		return fmt.Errorf("%w (refreshing the token failed too: %v)", err, rerr)
	case !renewed:
		return err
	}
	return client.JSON(ctx, build(token), out)
}

// boostyUnauthorized reports whether a failure was the platform saying the
// token is no good.
func boostyUnauthorized(err error) bool {
	var api *httpx.APIError
	return errors.As(err, &api) && api.Status == http.StatusUnauthorized
}

// bearer is the access token calls carry now.
func (b *Boosty) bearer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token
}

// boostyTokens is what the refresh endpoint answers with.
type boostyTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// renew trades the refresh token for a new pair, at most once per run.
//
// It reports renewed=false rather than an error when there is nothing to
// refresh with or when a refresh has already been tried, so the caller can
// surface the original 401 instead of burying it under a second failure.
//
// Boosty rotates the refresh token: the one that was sent is spent, and the
// answer carries its replacement. crier has nowhere to write that — the token
// is a secret the operator holds — so it goes out as a warning naming the new
// value, because the next run fails without it.
func (b *Boosty) renew(ctx context.Context) (token string, renewed bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.refreshed {
		return "", false, nil
	}
	if strings.TrimSpace(b.cfg.RefreshToken) == "" || strings.TrimSpace(b.cfg.DeviceID) == "" {
		// Not an error of its own: the 401 the caller already has says more
		// than this would, and this is the sentence that tells them why it
		// happened at all.
		b.log.Warn().Msg("boosty refused the access token and there is nothing to refresh it with; " +
			"a boosty access token expires, so set publish.boosty.refresh-token and " +
			"publish.boosty.device-id to have crier renew it")
		b.refreshed = true
		return "", false, nil
	}
	b.refreshed = true

	form := url.Values{
		"device_id":     {b.cfg.DeviceID},
		"device_os":     {"web"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {b.cfg.RefreshToken},
	}
	var out boostyTokens
	// The refresh creates nothing, so it is retryable like any other read.
	req := httpx.NewRequest(http.MethodPost, b.cfg.APIBaseURL, "oauth", "token/").Form(form)
	if err := b.client.JSON(ctx, req, &out); err != nil {
		return "", false, fmt.Errorf("refreshing the boosty access token: %w "+
			"(check publish.boosty.refresh-token and publish.boosty.device-id; "+
			"boosty spends a refresh token when it is used, so an old one is already dead)", err)
	}
	if out.AccessToken == "" {
		return "", false, fmt.Errorf("boosty accepted the refresh and returned no access token")
	}

	b.token = out.AccessToken
	if out.RefreshToken != "" && out.RefreshToken != b.cfg.RefreshToken {
		b.log.Warn().
			Str("refresh-token", out.RefreshToken).
			Int("expires_in", out.ExpiresIn).
			Msg("boosty issued a new refresh token and spent the old one; " +
				"update publish.boosty.refresh-token or the next run cannot renew")
	}
	b.log.Debug().Int("expires_in", out.ExpiresIn).Msg("refreshed the boosty access token")
	return b.token, true, nil
}

// boostyBlog is the blog as the read endpoint describes it.
type boostyBlog struct {
	Title   string `json:"title"`
	BlogURL string `json:"blogUrl"`
	IsOwner bool   `json:"isOwner"`
	Owner   struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"owner"`
	AccessRights struct {
		CanCreate bool `json:"canCreate"`
	} `json:"accessRights"`
}

// Ping reads the blog the post would land on.
//
// It is the cheapest question this API answers about a token and a blog at
// once: it fails when the token is wrong, and when it works it says whether
// the account behind the token may actually post there. A token that can read
// a blog it cannot write to is the commonest way a Boosty setup passes a
// credential check and then fails at the post.
func (b *Boosty) Ping(ctx context.Context) (Identity, error) {
	var blog boostyBlog
	err := b.do(ctx, b.client, func(token string) *httpx.Builder {
		return b.request(http.MethodGet, b.cfg.APIBaseURL, token, "v1", "blog", b.cfg.Blog)
	}, &blog)
	if err != nil {
		return Identity{}, err
	}

	id := Identity{
		ID:   firstNonEmpty(blog.BlogURL, b.cfg.Blog),
		Name: firstNonEmpty(blog.Title, blog.Owner.Name),
	}
	switch {
	case !blog.IsOwner && !blog.AccessRights.CanCreate:
		id.Note = "the token reads this blog and cannot post to it; it belongs to somebody who is not the author"
	case !blog.AccessRights.CanCreate:
		id.Note = "the token belongs to the author and the blog reports no right to create posts"
	default:
		id.Note = b.accessNote()
	}
	return id, nil
}

// accessNote says who will be able to read the post, because that is the
// setting a ping is most usefully asked about: a paid post that was meant to
// be free is not something the report would otherwise show.
func (b *Boosty) accessNote() string {
	switch boostyAccess(b.cfg.Access) {
	case BoostyAccessPaid:
		return fmt.Sprintf("posting behind a one-time price of %d %s",
			b.cfg.Price, boostyCurrency(b.cfg.Currency))
	case BoostyAccessLevel:
		return "posting for subscription level " + strings.TrimSpace(b.cfg.LevelID)
	default:
		return "posting free to everyone"
	}
}
