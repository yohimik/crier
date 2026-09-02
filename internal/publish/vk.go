package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// VK posts to a wall.
//
// Every call is a POST to {api-base-url}/method/{name} carrying the token and
// the API version as ordinary form fields, and every one of them answers 200 —
// success and failure alike. A failure is an "error" object in the body, so the
// status code says nothing here and the envelope is what has to be read.
//
// Media never goes to the API host. Each kind has its own two-step dance: ask
// for an upload server, POST the bytes there, and hand what that server said
// back to a save method, which turns it into the "photo123_456" string a post
// attaches. wall.post then takes the lot in one call, which is what makes a
// paged run one post with several pictures rather than several posts.
type VK struct {
	cfg    config.VK
	client *httpx.Client
	log    zerolog.Logger
}

func newVK(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.VK
	if err := require(c.APIBaseURL, "publish.vk.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.APIVersion, "publish.vk.api-version"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.vk.token"); err != nil {
		return nil, err
	}
	if c.OwnerID == 0 {
		// Zero is not a wall. Defaulting it to the token's own account would be
		// the one mistake that cannot be taken back: a post meant for a
		// community would land on somebody's personal page.
		return nil, fmt.Errorf(
			"publish.vk.owner-id is required: it is the wall to post on, negative for a community "+
				"such as -123456 and positive for a user (set it in the config file, %s or --%s)",
			config.EnvName("publish.vk.owner-id"), config.FlagName("publish.vk.owner-id"))
	}
	return &VK{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (v *VK) Name() string { return "vk" }

// VKAttachmentMax is how many attachments one wall post carries.
//
// wall.post documents the attachments parameter as taking at most ten media
// objects, and that is the whole of a post's capacity: the pages become one
// post of ten and then another, rather than a truncated one.
const VKAttachmentMax = 10

// Needs implements Publisher.
//
// VK takes the bytes, so it needs no staging. An animation is a document
// rather than a photo or a video, which is a different pair of methods and not
// a different kind of post.
func (v *VK) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageVideoAndGIF, MaxAttachments: VKAttachmentMax}
}

// community reports whether the wall belongs to a group rather than a person.
// The sign of the owner id is the whole of the distinction VK draws.
func (v *VK) community() bool { return v.cfg.OwnerID < 0 }

// groupID is the community's own id, which is the owner id without its sign.
func (v *VK) groupID() string { return strconv.Itoa(-v.cfg.OwnerID) }

// group adds the community id the upload-server methods want.
//
// It is not derivable from the token: photos.getWallUploadServer without a
// group_id hands out a slot on the caller's own wall, and a photo saved there
// cannot be attached to a community post.
func (v *VK) group(form url.Values) url.Values {
	if v.community() {
		form.Set("group_id", v.groupID())
	}
	return form
}

// vkError is the failure half of the envelope every method answers with.
type vkError struct {
	Code int    `json:"error_code"`
	Msg  string `json:"error_msg"`
}

// method calls one API method and decodes its response.
//
// The client is a parameter rather than the field, because which of the two
// clients a call gets is part of what the call means: wall.post is the post
// itself and must not be repeated, and everything before it may be.
//
// A 200 carrying an error object is a failure, and it is reported with the code
// as well as the message: VK's messages are terse and its codes are what the
// reference is indexed by.
func (v *VK) method(ctx context.Context, client *httpx.Client, name string, form url.Values, out any) error {
	form.Set("access_token", v.cfg.Token)
	form.Set("v", v.cfg.APIVersion)

	var env struct {
		Response json.RawMessage `json:"response"`
		Error    *vkError        `json:"error"`
	}
	req := httpx.NewRequest(http.MethodPost, v.cfg.APIBaseURL, "method", name).Form(form)
	if err := client.JSON(ctx, req, &env); err != nil {
		return fmt.Errorf("calling vk %s: %w", name, err)
	}
	if env.Error != nil {
		return fmt.Errorf("vk refused %s: error %d, %s", name, env.Error.Code, env.Error.Msg)
	}
	if out == nil {
		return nil
	}
	if len(env.Response) == 0 {
		return fmt.Errorf("vk answered %s with neither a response nor an error", name)
	}
	if err := json.Unmarshal(env.Response, out); err != nil {
		return fmt.Errorf("decoding the vk %s response: %w", name, err)
	}
	return nil
}

// Publish uploads every file the post carries and then makes the post.
func (v *VK) Publish(ctx context.Context, in Input) (Result, error) {
	arts := in.Sequence()
	if len(arts) > VKAttachmentMax {
		return Result{}, fmt.Errorf("a vk wall post takes %d attachments and this post has %d",
			VKAttachmentMax, len(arts))
	}

	attachments := make([]string, 0, len(arts))
	for n, a := range arts {
		var (
			att string
			err error
		)
		switch a.Kind {
		case render.KindVideo:
			att, err = v.uploadVideo(ctx, a, in.Caption)
		case render.KindGIF:
			att, err = v.uploadDoc(ctx, a)
		default:
			att, err = v.uploadPhoto(ctx, a)
		}
		if err != nil {
			if len(arts) > 1 {
				return Result{}, fmt.Errorf("attachment %d of %d: %w", n+1, len(arts), err)
			}
			return Result{}, err
		}
		attachments = append(attachments, att)
	}

	form := url.Values{"owner_id": {strconv.Itoa(v.cfg.OwnerID)}}
	if v.community() {
		// Without from_group a community post is signed by the person whose
		// token it was, which is visible on the post and is not what anybody
		// configuring a community wall asked for.
		form.Set("from_group", "1")
	}
	if in.Caption != "" {
		form.Set("message", in.Caption)
	}
	if len(attachments) > 0 {
		form.Set("attachments", strings.Join(attachments, ","))
	}

	var post struct {
		PostID int64 `json:"post_id"`
	}
	// wall.post is the act itself: a 5xx may mean the post was made and the
	// answer was lost, and repeating it would post twice.
	if err := v.method(ctx, v.client.NoRetry(), "wall.post", form, &post); err != nil {
		return Result{}, err
	}
	if post.PostID == 0 {
		return Result{}, fmt.Errorf("vk accepted the post and named no post id")
	}

	id := fmt.Sprintf("%d_%d", v.cfg.OwnerID, post.PostID)
	v.log.Debug().Str("post", id).Int("attachments", len(attachments)).Msg("posted to the vk wall")
	return Result{
		ID:    id,
		URL:   "https://vk.com/wall" + id,
		Extra: map[string]string{"attachments": strings.Join(attachments, ",")},
	}, nil
}

// vkUploaded is what a photo upload server answers with. The three fields are
// opaque and travel together: saveWallPhoto checks them against each other, so
// forwarding two of the three is a photo that never saves.
type vkUploaded struct {
	// Server is a number in the JSON and a string in the save call, which is
	// what json.Number is for.
	Server json.Number `json:"server"`
	Photo  string      `json:"photo"`
	Hash   string      `json:"hash"`
}

// uploadPhoto runs the three steps a picture takes, and returns the attachment
// string that names it.
func (v *VK) uploadPhoto(ctx context.Context, a render.Artifact) (string, error) {
	var server struct {
		UploadURL string `json:"upload_url"`
	}
	if err := v.method(ctx, v.client, "photos.getWallUploadServer", v.group(url.Values{}), &server); err != nil {
		return "", err
	}
	if server.UploadURL == "" {
		return "", fmt.Errorf("vk named no photo upload server")
	}

	var up vkUploaded
	req := httpx.NewRequest(http.MethodPost, server.UploadURL).
		Multipart(httpx.FilePart("photo", a.Path, a.ContentType))
	// The slot is single use, so a retry would push the bytes at a spent URL.
	if err := v.client.NoRetry().JSON(ctx, req, &up); err != nil {
		return "", fmt.Errorf("uploading the photo to vk: %w", err)
	}
	if up.Photo == "" || up.Photo == "[]" {
		// The upload server answers 200 either way and says so in the body: an
		// empty photo field, or the literal "[]", is how it reports a file it
		// would not take. Reading it as a success is how a post ends up with no
		// picture and no error.
		return "", fmt.Errorf("vk's upload server took %s and saved no photo "+
			"(it returned photo=%q); check the file is a JPEG or PNG within vk's size limit",
			a.Path, up.Photo)
	}

	var saved []struct {
		ID      int64 `json:"id"`
		OwnerID int64 `json:"owner_id"`
	}
	form := url.Values{
		"server": {up.Server.String()},
		"photo":  {up.Photo},
		"hash":   {up.Hash},
	}
	if err := v.method(ctx, v.client, "photos.saveWallPhoto", v.group(form), &saved); err != nil {
		return "", err
	}
	if len(saved) == 0 {
		return "", fmt.Errorf("vk saved the photo and named none")
	}
	att := fmt.Sprintf("photo%d_%d", saved[0].OwnerID, saved[0].ID)
	v.log.Debug().Str("attachment", att).Msg("uploaded a photo to vk")
	return att, nil
}

// uploadVideo saves a video slot, fills it, and names the result.
//
// video.save is the odd one out: it hands out the upload URL and the ids at
// once, so the attachment string is known before the bytes have gone anywhere.
func (v *VK) uploadVideo(ctx context.Context, a render.Artifact, caption string) (string, error) {
	form := url.Values{"name": {vkVideoName(caption)}}
	var save struct {
		UploadURL string `json:"upload_url"`
		OwnerID   int64  `json:"owner_id"`
		VideoID   int64  `json:"video_id"`
	}
	if err := v.method(ctx, v.client, "video.save", v.group(form), &save); err != nil {
		return "", err
	}
	if save.UploadURL == "" {
		return "", fmt.Errorf("vk named no video upload server")
	}

	req := httpx.NewRequest(http.MethodPost, save.UploadURL).
		Multipart(httpx.FilePart("video_file", a.Path, a.ContentType))
	if err := v.client.NoRetry().Discard(ctx, req); err != nil {
		return "", fmt.Errorf("uploading the video to vk: %w", err)
	}
	att := fmt.Sprintf("video%d_%d", save.OwnerID, save.VideoID)
	v.log.Debug().Str("attachment", att).Msg("uploaded a video to vk")
	return att, nil
}

// vkVideoName is the title a clip is saved under.
//
// video.save wants a name and shows it beside the clip, so the first line of
// the caption is the closest thing to a title the post has. A caption that is
// only a URL or is empty falls back to crier's own name rather than saving an
// untitled video.
func vkVideoName(caption string) string {
	line := strings.TrimSpace(strings.SplitN(caption, "\n", 2)[0])
	if line == "" {
		return "crier"
	}
	return line
}

// uploadDoc puts an animation up as a document.
//
// A GIF is not a photo to VK: saveWallPhoto re-encodes it into a still, and the
// only shape that keeps it moving on a wall is a document. That is why an
// animation is three different methods rather than a content type.
func (v *VK) uploadDoc(ctx context.Context, a render.Artifact) (string, error) {
	var server struct {
		UploadURL string `json:"upload_url"`
	}
	if err := v.method(ctx, v.client, "docs.getWallUploadServer", v.group(url.Values{}), &server); err != nil {
		return "", err
	}
	if server.UploadURL == "" {
		return "", fmt.Errorf("vk named no document upload server")
	}

	var up struct {
		File string `json:"file"`
	}
	req := httpx.NewRequest(http.MethodPost, server.UploadURL).
		Multipart(httpx.FilePart("file", a.Path, a.ContentType))
	if err := v.client.NoRetry().JSON(ctx, req, &up); err != nil {
		return "", fmt.Errorf("uploading the document to vk: %w", err)
	}
	if up.File == "" {
		return "", fmt.Errorf("vk's upload server took %s and saved no document", a.Path)
	}

	var saved struct {
		Type string `json:"type"`
		Doc  struct {
			ID      int64 `json:"id"`
			OwnerID int64 `json:"owner_id"`
		} `json:"doc"`
	}
	if err := v.method(ctx, v.client, "docs.save", url.Values{"file": {up.File}}, &saved); err != nil {
		return "", err
	}
	if saved.Doc.ID == 0 {
		return "", fmt.Errorf("vk saved the document and named no id")
	}
	att := fmt.Sprintf("doc%d_%d", saved.Doc.OwnerID, saved.Doc.ID)
	v.log.Debug().Str("attachment", att).Str("type", saved.Type).Msg("uploaded an animation to vk as a document")
	return att, nil
}

// Ping asks VK who the token and the owner id point at.
//
// Which question that is depends on the sign of the owner id, because the two
// setups fail in different places. A community wall is checked with
// groups.getById, which is the call that fails when the token has no rights to
// the group. A personal wall is checked with users.get and no user_ids at all,
// which VK answers with the account the token itself belongs to — the one thing
// a token can always be asked. Neither writes anything.
func (v *VK) Ping(ctx context.Context) (Identity, error) {
	if v.community() {
		return v.pingGroup(ctx)
	}
	return v.pingUser(ctx)
}

// vkGroup is one community, as groups.getById names it.
type vkGroup struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ScreenName string `json:"screen_name"`
}

func (v *VK) pingGroup(ctx context.Context) (Identity, error) {
	var raw json.RawMessage
	form := url.Values{"group_id": {v.groupID()}}
	if err := v.method(ctx, v.client, "groups.getById", form, &raw); err != nil {
		return Identity{}, err
	}
	groups, err := vkGroups(raw)
	if err != nil {
		return Identity{}, err
	}
	if len(groups) == 0 {
		return Identity{}, fmt.Errorf("vk named no community for publish.vk.owner-id %d", v.cfg.OwnerID)
	}

	g := groups[0]
	name := firstNonEmpty(g.Name, g.ScreenName)
	if g.Name != "" && g.ScreenName != "" {
		name = g.Name + " (@" + g.ScreenName + ")"
	}
	return Identity{
		ID:   strconv.FormatInt(g.ID, 10),
		Name: name,
		Note: "posting on the community wall, signed by the community",
	}, nil
}

// vkGroups reads the list of communities out of either shape the method has
// answered with.
//
// It was a bare array and became an object with a "groups" key partway through
// the 5.x line, and the version is configurable — so rather than tie the check
// to one API version, both are accepted. The list is the same either way.
func vkGroups(raw json.RawMessage) ([]vkGroup, error) {
	var list []vkGroup
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var wrapped struct {
		Groups []vkGroup `json:"groups"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decoding the vk groups.getById response: %w", err)
	}
	return wrapped.Groups, nil
}

func (v *VK) pingUser(ctx context.Context) (Identity, error) {
	// No user_ids: VK reads the token's own account, which is what makes this
	// a credential check rather than a lookup of somebody in particular.
	var users []struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := v.method(ctx, v.client, "users.get", url.Values{}, &users); err != nil {
		return Identity{}, err
	}
	if len(users) == 0 {
		return Identity{}, fmt.Errorf("vk accepted the token and named no account")
	}

	u := users[0]
	id := Identity{
		ID:   strconv.FormatInt(u.ID, 10),
		Name: strings.TrimSpace(u.FirstName + " " + u.LastName),
	}
	if strconv.FormatInt(u.ID, 10) != strconv.Itoa(v.cfg.OwnerID) {
		// A token for one account posting on another's wall is legal and is
		// almost always a mistyped owner id, so it is worth saying rather than
		// finding out from where the post landed.
		id.Note = fmt.Sprintf("posting on the wall of user %d, which is not this token's own account",
			v.cfg.OwnerID)
	}
	return id, nil
}
