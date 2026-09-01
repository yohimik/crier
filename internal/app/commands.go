package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/stage"
)

// writeJSON prints a value as one indented JSON document.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- render ----------------------------------------------------------------

// RenderReport is what `crier render` prints.
type RenderReport struct {
	Variant string   `json:"variant"`
	Kind    string   `json:"kind"`
	Files   []string `json:"files"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
}

func (a App) runRender(ctx context.Context, args []string) error {
	var (
		variantFor string
		asJSON     bool
	)
	s, err := a.load("render", args, func(fs *flag.FlagSet) {
		fs.StringVar(&variantFor, "render-variant", "",
			"render the variant a platform would get, rather than the base layout")
		fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
	})
	if err != nil || s == nil {
		return err
	}
	if err := require(s.Config.Render.Template, "render.template"); err != nil {
		return err
	}

	p, err := NewPipeline(PipelineOptions{
		Config: s.Config, Logger: s.Log, Client: s.Client, Stdin: a.Stdin,
	})
	if err != nil {
		return err
	}
	defer p.Cleanup(ctx)

	data, err := p.Data()
	if err != nil {
		return err
	}

	v := BaseVariant(s.Config)
	if variantFor != "" {
		if config.LayoutOf(&s.Config.Publish, variantFor) == nil {
			return failf(ExitUsage, "--render-variant: %q is not a platform crier knows (%s)",
				variantFor, strings.Join(config.Platforms, ", "))
		}
		vs := Variants(s.Config, []string{variantFor})
		v = vs[0]
	}

	format, err := config.ParseFormat(s.Config.Render.Format)
	if err != nil {
		return fail(ExitConfig, err)
	}
	arts, err := p.Render(ctx, v, data, []config.Format{format})
	if err != nil {
		return err
	}

	rep := RenderReport{Variant: v.Name(), Kind: string(render.KindImage)}
	if arts.Video != nil {
		rep.Kind = string(render.KindVideo)
		path, err := placeOutput(s.Config.Render.Output, arts.Video.Path, ".mp4")
		if err != nil {
			return fail(ExitRender, err)
		}
		rep.Files = []string{path}
		rep.Width, rep.Height = arts.Video.Width, arts.Video.Height
	} else {
		art := arts.Images[format]
		path, err := placeOutput(s.Config.Render.Output, art.Path, format.Ext())
		if err != nil {
			return fail(ExitRender, err)
		}
		rep.Files = []string{path}
		rep.Width, rep.Height = art.Width, art.Height
	}

	s.Log.Info().Str("file", rep.Files[0]).Int("width", rep.Width).Int("height", rep.Height).
		Str("variant", rep.Variant).Msg("rendered")

	if asJSON {
		return writeJSON(a.Stdout, rep)
	}
	for _, f := range rep.Files {
		fmt.Fprintln(a.Stdout, f)
	}
	return nil
}

// placeOutput moves a rendered file to where the operator asked for it.
//
// When no output path was configured the file stays where it was written and
// is copied out of the working directory, because that directory is removed on
// cleanup and a path that no longer exists is not a useful thing to print.
func placeOutput(want, produced, ext string) (string, error) {
	if want == "" {
		want = filepath.Join(".", "crier-output"+ext)
	}
	if want == produced {
		return want, nil
	}
	if dir := filepath.Dir(want); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := copyFile(produced, want); err != nil {
		return "", err
	}
	return want, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// --- publish ---------------------------------------------------------------

// PublishReport is what `crier publish` prints.
type PublishReport struct {
	DryRun   bool               `json:"dryRun"`
	Variants []VariantReport    `json:"variants"`
	Results  []publish.Outcome  `json:"results,omitempty"`
	Plan     []PlannedPublisher `json:"plan,omitempty"`
}

// VariantReport describes one rendered variant.
type VariantReport struct {
	Name      string   `json:"name"`
	Platforms []string `json:"platforms"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Files     []string `json:"files"`
	URL       string   `json:"url,omitempty"`
}

// PlannedPublisher is one line of a dry run.
type PlannedPublisher struct {
	Platform string `json:"platform"`
	Variant  string `json:"variant"`
	File     string `json:"file"`
	NeedsURL bool   `json:"needsUrl"`
	Caption  string `json:"caption"`
}

func (a App) runPublish(ctx context.Context, args []string) error {
	var asJSON bool
	s, err := a.load("publish", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
	})
	if err != nil || s == nil {
		return err
	}
	cfg := s.Config
	if err := require(cfg.Render.Template, "render.template"); err != nil {
		return err
	}

	enabled := publish.Enabled(cfg)
	if len(enabled) == 0 {
		return failf(ExitConfig, "no platform is enabled; set publish.<platform>.enabled for at least one of %s",
			strings.Join(config.Platforms, ", "))
	}

	publishers, err := publish.Build(cfg, publish.Deps{
		Client: s.Client, Logger: s.Log, UserAgent: userAgent(),
	})
	if err != nil {
		return fail(ExitConfig, err)
	}
	byName := map[string]publish.Publisher{}
	for _, pub := range publishers {
		byName[pub.Name()] = pub
	}

	p, err := NewPipeline(PipelineOptions{
		Config: cfg, Logger: s.Log, Client: s.Client, Stdin: a.Stdin,
	})
	if err != nil {
		return err
	}
	defer p.Cleanup(ctx)

	data, err := p.Data()
	if err != nil {
		return err
	}
	if err := ResolveTexts(cfg, data); err != nil {
		return err
	}

	// A video that a platform cannot take is a configuration mistake, and
	// saying so before anything is rendered saves the whole render.
	if cfg.Render.Video.Enabled {
		for _, pub := range publishers {
			if !pub.Needs().Accepts(render.KindVideo) {
				return failf(ExitConfig,
					"render.video.enabled is set but %s cannot post video; disable it or turn the video off",
					pub.Name())
			}
		}
	}

	// A platform that can only be given a URL, with nothing configured to
	// produce one, is a configuration mistake rather than a publish failure —
	// and saying so now saves the render as well as the confusion.
	if stage.Mode(strings.ToLower(strings.TrimSpace(cfg.Stage.Mode))) == stage.ModeNone {
		for _, pub := range publishers {
			if pub.Needs().URL {
				return failf(ExitConfig,
					"%s can only be given a URL for the media, and stage.mode is none; "+
						"set stage.mode to s3, server or url", pub.Name())
			}
		}
	}

	stager, err := a.stager(cfg, s, p)
	if err != nil {
		return err
	}

	report := PublishReport{DryRun: cfg.Publish.DryRun}
	var jobs []publish.Job

	for _, v := range Variants(cfg, enabled) {
		group := make([]publish.Publisher, 0, len(v.Platforms))
		needsURL, needsPoster := false, false
		for _, name := range v.Platforms {
			pub := byName[name]
			group = append(group, pub)
			if pub.Needs().URL {
				needsURL = true
			}
			if name == "reddit" && cfg.Render.Video.Enabled {
				needsPoster = true
			}
		}

		arts, err := p.Render(ctx, v, data, FormatsFor(cfg, group))
		if err != nil {
			return err
		}
		if !cfg.Publish.DryRun {
			if err := p.Stage(ctx, stager, &arts, needsURL, needsPoster); err != nil {
				return err
			}
		}

		vr := VariantReport{Name: v.Name(), Platforms: v.Platforms, URL: arts.URL}
		for _, art := range sortedArtifacts(arts) {
			vr.Files = append(vr.Files, art.Path)
			vr.Width, vr.Height = art.Width, art.Height
		}
		report.Variants = append(report.Variants, vr)

		for _, pub := range group {
			art, err := arts.Primary(pub.Needs())
			if err != nil {
				return failf(ExitConfig, "%s: %v", pub.Name(), err)
			}
			caption, err := CaptionFor(cfg, pub.Name(), data)
			if err != nil {
				return err
			}
			in := publish.Input{
				Artifact:  art,
				URL:       arts.URL,
				Caption:   caption,
				Poster:    arts.Poster,
				PosterURL: arts.PosterURL,
			}
			if cfg.Publish.DryRun {
				report.Plan = append(report.Plan, PlannedPublisher{
					Platform: pub.Name(), Variant: v.Name(), File: art.Path,
					NeedsURL: pub.Needs().URL, Caption: caption,
				})
				continue
			}
			jobs = append(jobs, publish.Job{Publisher: pub, Input: in})
		}
	}

	if cfg.Publish.DryRun {
		s.Log.Info().Int("platforms", len(report.Plan)).Msg("dry run: nothing was sent")
		return a.printPublish(report, asJSON)
	}

	res := publish.RunAll(ctx, jobs, cfg.Publish.Concurrency, s.Log)
	report.Results = res.Outcomes
	if err := a.printPublish(report, asJSON); err != nil {
		return err
	}

	switch {
	case res.Failed() == 0:
		return nil
	case res.Succeeded() == 0:
		return fail(ExitPublish, res.Err())
	default:
		return fail(ExitPartial, res.Err())
	}
}

// stager builds the staging strategy and registers its shutdown.
func (a App) stager(cfg *config.Config, s *setup, p *Pipeline) (stage.Stager, error) {
	st, err := stage.New(stage.Options{
		Config: cfg.Stage,
		Logger: s.Log,
		Client: s.Client,
	})
	if err != nil {
		return nil, fail(ExitStaging, err)
	}
	p.onCleanup(st.Close)
	return st, nil
}

func sortedArtifacts(a Artifacts) []render.Artifact {
	var out []render.Artifact
	if a.Video != nil {
		out = append(out, *a.Video)
	}
	formats := make([]config.Format, 0, len(a.Images))
	for f := range a.Images {
		formats = append(formats, f)
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
	for _, f := range formats {
		out = append(out, a.Images[f])
	}
	return out
}

func (a App) printPublish(rep PublishReport, asJSON bool) error {
	if asJSON {
		return writeJSON(a.Stdout, rep)
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	if rep.DryRun {
		fmt.Fprintln(w, "PLATFORM\tVARIANT\tFILE\tURL NEEDED\tCAPTION")
		for _, pl := range rep.Plan {
			fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", pl.Platform, pl.Variant, pl.File, pl.NeedsURL, oneLine(pl.Caption))
		}
		return w.Flush()
	}
	fmt.Fprintln(w, "PLATFORM\tSTATUS\tID\tURL")
	for _, o := range rep.Results {
		status := "ok"
		detail := o.URL
		if !o.OK {
			status = "failed"
			detail = oneLine(o.Error)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", o.Platform, status, o.ID, detail)
	}
	return w.Flush()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

// --- platforms -------------------------------------------------------------

// PlatformInfo is one row of `crier platforms`.
type PlatformInfo struct {
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	Ready    bool     `json:"ready"`
	Problem  string   `json:"problem,omitempty"`
	NeedsURL bool     `json:"needsUrl"`
	Formats  []string `json:"formats,omitempty"`
	Kinds    []string `json:"kinds,omitempty"`
}

func (a App) runPlatforms(args []string) error {
	var asJSON bool
	s, err := a.load("platforms", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print the list as JSON")
	})
	if err != nil || s == nil {
		return err
	}

	var rows []PlatformInfo
	for _, name := range config.Platforms {
		info := PlatformInfo{Name: name, Enabled: enabledFor(s.Config, name)}
		// Building one platform on its own says whether it is configured, and
		// the error says what is missing.
		one := *s.Config
		enableOnly(&one, name)
		built, err := publish.Build(&one, publish.Deps{Client: s.Client, Logger: s.Log})
		switch {
		case err != nil:
			info.Problem = err.Error()
		case len(built) == 1:
			info.Ready = true
			needs := built[0].Needs()
			info.NeedsURL = needs.URL
			for _, f := range needs.Formats {
				info.Formats = append(info.Formats, string(f))
			}
			for _, k := range needs.Kinds {
				info.Kinds = append(info.Kinds, string(k))
			}
		}
		rows = append(rows, info)
	}

	if asJSON {
		return writeJSON(a.Stdout, rows)
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLATFORM\tENABLED\tCONFIGURED\tNEEDS URL\tKINDS\tPROBLEM")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%t\t%t\t%t\t%s\t%s\n",
			r.Name, r.Enabled, r.Ready, r.NeedsURL, strings.Join(r.Kinds, ","), oneLine(r.Problem))
	}
	return w.Flush()
}

func enabledFor(cfg *config.Config, name string) bool {
	for _, n := range publish.Enabled(cfg) {
		if n == name {
			return true
		}
	}
	return false
}

// enableOnly turns on exactly one platform, so it can be validated in
// isolation.
func enableOnly(cfg *config.Config, name string) {
	p := &cfg.Publish
	p.Instagram.Enabled = name == "instagram"
	p.Facebook.Enabled = name == "facebook"
	p.TikTok.Enabled = name == "tiktok"
	p.Telegram.Enabled = name == "telegram"
	p.X.Enabled = name == "x"
	p.Mastodon.Enabled = name == "mastodon"
	p.Discord.Enabled = name == "discord"
	p.LinkedIn.Enabled = name == "linkedin"
	p.Reddit.Enabled = name == "reddit"
}

// --- config ----------------------------------------------------------------

// Redacted is the placeholder a secret is printed as.
const Redacted = "********"

// ConfigReport is what `crier config` prints.
type ConfigReport struct {
	File   string         `json:"file,omitempty"`
	Dir    string         `json:"dir,omitempty"`
	Files  []string       `json:"files,omitempty"`
	Values map[string]any `json:"values"`
}

func (a App) runConfig(args []string) error {
	var (
		asJSON  bool
		showAll bool
	)
	s, err := a.load("config", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print the configuration as JSON")
		fs.BoolVar(&showAll, "all", false, "include keys left at their default")
	})
	if err != nil || s == nil {
		return err
	}

	defaults := config.Defaults()
	defaultValues := config.Values(&defaults)
	values := config.Values(s.Config)
	secrets := map[string]bool{}
	for _, d := range config.Registry() {
		secrets[d.Key] = d.Secret
	}

	out := map[string]any{}
	for key, v := range values {
		if !showAll && sameValue(v, defaultValues[key]) {
			continue
		}
		if secrets[key] && !isZero(v) {
			out[key] = Redacted
			continue
		}
		out[key] = v
	}

	rep := ConfigReport{File: s.Result.File, Dir: s.Result.Dir, Files: s.Result.Files, Values: out}
	if asJSON {
		return writeJSON(a.Stdout, rep)
	}
	if rep.File != "" {
		fmt.Fprintf(a.Stdout, "# from %s\n", rep.File)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%v\n", k, out[k])
	}
	return w.Flush()
}

func sameValue(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) }

func isZero(v any) bool {
	switch t := v.(type) {
	case string:
		return t == ""
	case int:
		return t == 0
	case bool:
		return !t
	case []string:
		return len(t) == 0
	default:
		return v == nil
	}
}

// require reports a missing key as a configuration error.
func require(value, key string) error {
	if strings.TrimSpace(value) != "" {
		return nil
	}
	return failf(ExitConfig, "%s is required (set it in the config file, %s or --%s)",
		key, config.EnvName(key), config.FlagName(key))
}
