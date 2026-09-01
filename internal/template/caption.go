package template

import (
	"bytes"
	"fmt"
	"strings"
	texttemplate "text/template"
)

// PlatformKey is the name the platform is bound to inside a caption template.
const PlatformKey = "Platform"

// DataKey is where a data document that is not an object is bound, so a
// caption can still reach it as {{.Data}}.
const DataKey = "Data"

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

// RenderCaption executes a caption with the engine's own function set, which
// is what puts the run's random source in reach of a caption as well as a
// layout.
func (e *Engine) RenderCaption(tmpl string, data any, platform string) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	t, err := texttemplate.New("caption").
		Funcs(texttemplate.FuncMap(e.funcs)).
		Option("missingkey=error").
		Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing caption template: %w", err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, captionData(data, platform)); err != nil {
		return "", fmt.Errorf("executing caption template: %w", err)
	}
	return out.String(), nil
}

// captionData binds the platform name alongside the document.
//
// A document that is an object gets the extra key merged in. A document that
// is a list or a scalar cannot hold a key at all, so it is bound under Data and
// the platform sits beside it.
func captionData(data any, platform string) any {
	if obj, ok := data.(map[string]any); ok {
		out := make(map[string]any, len(obj)+1)
		for k, v := range obj {
			out[k] = v
		}
		out[PlatformKey] = platform
		return out
	}
	if data == nil {
		return map[string]any{PlatformKey: platform}
	}
	return map[string]any{PlatformKey: platform, DataKey: data}
}
