package app

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/procutil"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/stage"
)

// PingResult is one row of `crier ping`.
type PingResult struct {
	// Target is the platform's name, or "stage" for the object store.
	Target string `json:"target"`
	// OK says the credentials were accepted.
	OK bool `json:"ok"`
	// Account is who the platform said the credentials belong to.
	Account string `json:"account,omitempty"`
	// Note is a caveat worth reporting alongside a success.
	Note string `json:"note,omitempty"`
	// Error is why it failed.
	Error string `json:"error,omitempty"`
	// Millis is how long the round trip took.
	Millis int64 `json:"ms"`
}

// PingReport is what `crier ping` prints.
type PingReport struct {
	Results []PingResult `json:"results"`
}

// runPing checks every enabled platform's credentials without posting.
//
// It is the command that answers "is this set up right" without the answer
// being a real post on a real feed. Every call it makes is a read: an identity
// endpoint per platform, a HEAD on the bucket for staging.
//
// There is no --dry-run here, and that is deliberate — ping *is* the dry run
// for credentials, and a dry ping would check nothing at all.
func (a App) runPing(ctx context.Context, args []string) error {
	var asJSON bool
	s, err := a.load("ping", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
	})
	if err != nil || s == nil {
		return err
	}
	cfg := s.Config

	enabled := publish.Enabled(cfg)
	if len(enabled) == 0 {
		return failf(ExitConfig, "no platform is enabled; set publish.<platform>.enabled for at least one of %s",
			strings.Join(config.Platforms, ", "))
	}

	// The operator's own files are checked first, and on their own. Building a
	// publisher refuses a file that is not audio or is not an MP4, so a check
	// that ran after the build would never be reached by the very configuration
	// it exists to explain.
	files := append(pingMusic(cfg), pingCoverStory(ctx, cfg, s.Log)...)
	files = append(files, pingLeadVideos(cfg)...)

	// The same constructors publish uses, so a configuration ping accepts is a
	// configuration publish would get as far as the network with.
	publishers, err := publish.Build(cfg, publish.Deps{
		Client: s.Client, Logger: s.Log, UserAgent: userAgent(), Dir: s.Result.Dir,
	})
	if err != nil {
		// The rows go out anyway: "music failed: jingle.mp3 does not begin like
		// an audio file" says which line to change, and the joined build error
		// underneath says the rest.
		if len(files) > 0 {
			_ = a.printPing(PingReport{Results: files}, asJSON)
		}
		return fail(ExitConfig, err)
	}

	report := publish.PingAll(ctx, publishers, cfg.Publish.Concurrency, s.Log)

	rep := PingReport{}
	for _, o := range report.Outcomes {
		rep.Results = append(rep.Results, PingResult{
			Target:  o.Platform,
			OK:      o.OK,
			Account: o.ID,
			Note:    o.Extra["note"],
			Error:   o.Error,
			Millis:  o.Elapsed.Milliseconds(),
		})
	}

	// The audio and the opening clip, for the same reason a token is checked:
	// they are things that have to be right, and finding out from the platform
	// means finding out after the post.
	rep.Results = append(rep.Results, files...)

	// Staging is checked too, because a bucket crier cannot write to fails a
	// publish just as completely as a bad token does — and it fails after the
	// render, which is the expensive half.
	if row, ok := a.pingStage(ctx, s); ok {
		rep.Results = append(rep.Results, row)
	}

	if err := a.printPing(rep, asJSON); err != nil {
		return err
	}

	failed, succeeded := 0, 0
	for _, r := range rep.Results {
		if r.OK {
			succeeded++
		} else {
			failed++
		}
	}
	switch {
	case failed == 0:
		return nil
	case succeeded == 0:
		return fail(ExitPublish, report.Err())
	default:
		return fail(ExitPartial, pingErr(rep))
	}
}

// pingMusic checks the audio files the configuration names, one row each.
//
// The file is not a credential, so nothing is asked of any platform: the check
// is that the file is there, that it can be read, and that its first bytes are
// one of the containers crier sends. A run with no music configured produces
// no rows at all, which is why this is not a target that is always present.
//
// A file no enabled platform can carry is reported as a success with a note
// rather than as a failure. The file is fine; the configuration around it is
// the thing worth looking at, and only the operator can say whether that was
// the intention.
func pingMusic(cfg *config.Config) []PingResult {
	var out []PingResult
	for _, c := range publish.CheckMusic(cfg) {
		target := "music"
		if c.Key != "publish.music-file" {
			target = "music:" + strings.TrimSuffix(strings.TrimPrefix(c.Key, "publish."), ".music-file")
		}
		row := PingResult{Target: target}
		if c.Err != nil {
			row.Error = c.Err.Error()
			out = append(out, row)
			continue
		}
		row.OK = true
		row.Account = c.Audio.Name
		row.Note = c.Describe()
		out = append(out, row)
	}
	return out
}

// pingCoverStory checks the encoder and every possible seeded audio choice, rather than only
// the track this invocation happened to pick. A later publish can use another
// seed, so ping must establish that the whole configured pool is usable.
func pingCoverStory(ctx context.Context, cfg *config.Config, log zerolog.Logger) []PingResult {
	if !cfg.Publish.Instagram.Enabled || !cfg.Publish.Instagram.CoverStory {
		return nil
	}
	bin := cfg.Render.Video.FFmpegBin
	if strings.TrimSpace(bin) == "" {
		bin = "ffmpeg"
	}
	encoder := PingResult{Target: "cover-story-encoder"}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	proc, err := procutil.Start(checkCtx, procutil.Options{Name: "ffmpeg", Bin: bin, Args: []string{"-version"}, Logger: log})
	if err == nil {
		err = proc.Wait()
	}
	cancel()
	if err != nil {
		encoder.Error = err.Error()
	} else {
		encoder.OK, encoder.Account = true, filepath.Base(bin)
		encoder.Note = "encodes the generated Instagram cover story"
	}
	paths := cfg.Render.Video.AudioPool
	if len(paths) == 0 {
		paths = []string{cfg.Render.Video.Audio}
	}
	out := make([]PingResult, 0, len(paths)+1)
	out = append(out, encoder)
	for i, path := range paths {
		target := "cover-story-music"
		if len(paths) > 1 {
			target = fmt.Sprintf("cover-story-music:%d", i+1)
		}
		row := PingResult{Target: target}
		audio, err := publish.SniffAudio(path)
		if err != nil {
			row.Error = err.Error()
		} else {
			row.OK, row.Account = true, audio.Name
			row.Note = "used by the generated Instagram cover story"
		}
		out = append(out, row)
	}
	return out
}

// pingLeadVideos checks the clips a configuration names, one row per platform.
//
// The same shape as the audio rows and for the same reason: building a
// publisher refuses a clip that is not an MP4, so a check that ran after the
// build would never be reached by the configuration it exists to explain.
func pingLeadVideos(cfg *config.Config) []PingResult {
	var out []PingResult
	for _, c := range publish.CheckLeadVideos(cfg) {
		row := PingResult{Target: "lead-video:" + c.Platform}
		if c.Err != nil {
			row.Error = c.Err.Error()
			out = append(out, row)
			continue
		}
		row.OK = true
		row.Account = c.Video.Name
		row.Note = c.Describe()
		out = append(out, row)
	}
	return out
}

// pingStage checks the stager when it holds a credential.
//
// The modes that hold none — none, url, server — return false rather than a
// passing row: reporting "ok" for something that was never checked is worse
// than saying nothing.
func (a App) pingStage(ctx context.Context, s *setup) (PingResult, bool) {
	st, err := stage.New(stage.Options{Config: s.Config.Stage, Logger: s.Log, Client: s.Client})
	if err != nil {
		return PingResult{Target: "stage", Error: err.Error(), Millis: 0}, true
	}
	defer func() { _ = st.Close(context.WithoutCancel(ctx)) }()

	pinger, ok := st.(stage.Pinger)
	if !ok {
		s.Log.Debug().Str("mode", st.Name()).Msg("this staging mode holds no credentials to check")
		return PingResult{}, false
	}

	start := time.Now()
	what, err := pinger.Ping(ctx)
	elapsed := time.Since(start)
	row := PingResult{Target: "stage:" + st.Name(), Millis: elapsed.Milliseconds()}
	if err != nil {
		row.Error = err.Error()
		s.Log.Error().Str("target", row.Target).Dur("elapsed", elapsed).Err(err).Msg("staging is not reachable")
		return row, true
	}
	row.OK = true
	row.Account = what
	s.Log.Info().Str("target", row.Target).Str("reached", what).Dur("elapsed", elapsed).
		Msg("staging is reachable")
	return row, true
}

func (a App) printPing(rep PingReport, asJSON bool) error {
	if asJSON {
		return writeJSON(a.Stdout, rep)
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tSTATUS\tACCOUNT\tMS\tDETAIL")
	for _, r := range rep.Results {
		status, detail := "ok", r.Note
		if !r.OK {
			status, detail = "failed", r.Error
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", r.Target, status, r.Account, r.Millis, oneLine(detail))
	}
	return w.Flush()
}

// pingErr names the targets that failed, for the exit message.
func pingErr(rep PingReport) error {
	var parts []string
	for _, r := range rep.Results {
		if !r.OK {
			parts = append(parts, r.Target+": "+oneLine(r.Error))
		}
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}
