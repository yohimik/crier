package config

import "strings"

// Fit is how a rendered image is made to match a platform's frame.
//
// The platforms disagree about whose job this is. Instagram crops a story to
// 1080×1920 on its own servers and tells nobody where it cut; Telegram shows
// whatever it is given. Doing it here is what makes the answer visible before
// the post goes out, and the same on every platform that asks for it.
//
// It lives with the configuration rather than with the imaging because it is a
// value somebody writes in a file. The geometry that acts on it is in
// internal/render.
type Fit string

const (
	// FitNone sends the render as it is. The default, and what every platform
	// did before this existed.
	FitNone Fit = "none"
	// FitCover scales to fill the frame and crops the overflow, centred. What
	// a story wants: no bars, and the middle of the card survives.
	FitCover Fit = "cover"
	// FitContain scales to fit inside the frame and fills the rest with the
	// background colour. Nothing is lost and the shape is wrong on purpose.
	FitContain Fit = "contain"
	// FitStretch ignores the aspect ratio. Almost always a mistake, and
	// occasionally exactly what somebody wants.
	FitStretch Fit = "stretch"
)

// Fits are the modes, in the order a message listing them should use.
var Fits = []Fit{FitNone, FitCover, FitContain, FitStretch}

// ParseFit reads a fit mode, tolerating case and surrounding space. An empty
// value is FitNone, which is what an unset key means.
func ParseFit(s string) (Fit, bool) {
	switch Fit(strings.ToLower(strings.TrimSpace(s))) {
	case "", FitNone:
		return FitNone, true
	case FitCover:
		return FitCover, true
	case FitContain:
		return FitContain, true
	case FitStretch:
		return FitStretch, true
	default:
		return FitNone, false
	}
}

// FitNames are the modes as text, for an error that has to list them.
func FitNames() []string {
	out := make([]string, 0, len(Fits))
	for _, f := range Fits {
		out = append(out, string(f))
	}
	return out
}
