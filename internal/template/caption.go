package template

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yohimik/crier/internal/template/exec"
)

// PlatformKey is the name the platform is bound to inside a caption template.
const PlatformKey = "Platform"

// DataKey is where a data document that is not an object is bound, so a
// caption can still reach it as {{.Data}}.
const DataKey = "Data"

// The paging keys let a caption write "2 of 3" when a page list was too long
// for one post. They are always bound, and read 1 of 1 when nothing paginated.
const (
	PostKey  = "Post"
	PostsKey = "Posts"
	PageKey  = "Page"
	PagesKey = "Pages"
)

// RenderCaption executes a caption, title or alt-text as a Go template.
//
// The point is that a post's text is configuration like everything else, and
// configuration that can say `New release {{.Version}} on {{.Platform}}` says
// once what would otherwise be written out per platform by hand. The data is
// the same document the HTML template was rendered with, plus the platform's
// own name.
//
// A string with no action in it is returned unchanged, so a plain caption
// costs no parse and cannot fail. Missing keys are errors rather than the
// "<no value>" that would otherwise be posted publicly.
func RenderCaption(tmpl string, data any, platform string) (string, error) {
	return New().RenderCaption(tmpl, data, platform)
}

// Paging is where a post sits in a paged run, bound into a caption so one line
// of configuration can write "2 of 3".
//
// A run that fits in one post is post 1 of 1 carrying page 1 of 1, so a caption
// that mentions these reads sensibly whether or not anything paginated.
type Paging struct {
	// Post and Posts are this post's place in the sequence.
	Post, Posts int
	// Page is the first page this post carries; Pages is the run's total.
	Page, Pages int
}

// OnePost is the paging of a run that fitted in a single post.
func OnePost() Paging { return Paging{Post: 1, Posts: 1, Page: 1, Pages: 1} }

// RenderCaption executes a caption with the engine's own function set, which
// is what puts the run's random source in reach of a caption as well as a
// layout.
func (e *Engine) RenderCaption(tmpl string, data any, platform string) (string, error) {
	return e.RenderCaptionAt(tmpl, data, platform, OnePost())
}

// RenderCaptionAt is RenderCaption for one post of a paged run.
func (e *Engine) RenderCaptionAt(tmpl string, data any, platform string, at Paging) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	t, err := exec.New("caption").
		Funcs(e.execFuncs()).
		Option("missingkey=error").
		Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing caption template: %w", err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, captionData(data, platform, at)); err != nil {
		return "", fmt.Errorf("executing caption template: %w", err)
	}
	return out.String(), nil
}

// captionData binds the platform name and the paging alongside the document.
//
// A document that is an object gets the extra keys merged in. A document that
// is a list or a scalar cannot hold a key at all, so it is bound under Data and
// the rest sits beside it.
//
// The bound keys win over a document's own of the same name, which is how
// .Platform has always worked. A data document with its own Post key is
// shadowed here.
func captionData(data any, platform string, at Paging) any {
	out := map[string]any{}
	if obj, ok := data.(map[string]any); ok {
		for k, v := range obj {
			out[k] = v
		}
	} else if data != nil {
		out[DataKey] = data
	}
	out[PlatformKey] = platform
	out[PostKey] = at.Post
	out[PostsKey] = at.Posts
	out[PageKey] = at.Page
	out[PagesKey] = at.Pages
	return out
}
