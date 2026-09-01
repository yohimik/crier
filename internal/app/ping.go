package app

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yohimik/crier/internal/config"
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

	// The same constructors publish uses, so a configuration ping accepts is a
	// configuration publish would get as far as the network with.
	publishers, err := publish.Build(cfg, publish.Deps{
		Client: s.Client, Logger: s.Log, UserAgent: userAgent(),
	})
	if err != nil {
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
