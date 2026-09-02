package app

import (
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/template"
)

// PostsFor turns one run's page list into the posts one platform will receive.
//
// The page list is the run's, not the platform's: every platform gets the same
// pages in the same order, and the only thing a platform may do to that list is
// cut it into the sizes it accepts. Nothing here reorders, skips or merges
// pages, which is what makes a carousel at Instagram and a pair of media groups
// at Telegram tell the same story in the same sequence.
//
// A platform that takes one file at a time therefore gets one post per page,
// published in order — which is exactly what Instagram stories are, so they
// need no special case.
func PostsFor(engine *template.Engine, cfg *config.Config, pub publish.Publisher,
	arts Artifacts, data any,
) ([]publish.Input, error) {
	needs := pub.Needs()
	files, err := arts.Sequence(needs)
	if err != nil {
		return nil, failf(ExitConfig, "%s: %v", pub.Name(), err)
	}
	urls := arts.URLs()
	if arts.Video != nil {
		// A clip is one file however many pages the stills would have been.
		urls = nil
		if u := arts.URL(); u != "" {
			urls = []string{u}
		}
	}

	capacity := capacityFor(cfg, pub.Name(), needs)
	batches := publish.Batches(files, urls, capacity)

	out := make([]publish.Input, 0, len(batches))
	for i, b := range batches {
		at := template.Paging{
			Post: i + 1, Posts: len(batches),
			Page: b.First + 1, Pages: len(files),
		}
		caption, err := CaptionAt(engine, cfg, pub.Name(), data, at)
		if err != nil {
			return nil, err
		}
		in := publish.Input{
			Artifact:  b.Artifacts[0],
			Artifacts: b.Artifacts,
			URLs:      b.URLs,
			Caption:   caption,
			Poster:    arts.Poster,
			PosterURL: arts.PosterURL,
			Post:      at.Post, Posts: at.Posts,
			Page: at.Page, Pages: at.Pages,
		}
		if len(b.URLs) > 0 {
			in.URL = b.URLs[0]
		}
		out = append(out, in)
	}
	return out, nil
}

// capacityFor is how many pages this platform takes in one post: its own limit,
// or a smaller one the operator asked for.
//
// The override can only lower it. A configuration that asked Instagram for
// twenty would be refused by Instagram, which is a worse way to find out than
// being told here — so the platform's own number is the ceiling.
func capacityFor(cfg *config.Config, platform string, needs publish.Needs) int {
	limit := needs.Capacity()
	want := maxAttachmentsOf(cfg, platform)
	if want > 0 && want < limit {
		return want
	}
	return limit
}

// maxAttachmentsOf reads publish.<platform>.max-attachments.
func maxAttachmentsOf(cfg *config.Config, platform string) int {
	if l := config.LayoutOf(&cfg.Publish, platform); l != nil {
		return l.MaxAttachments
	}
	return 0
}
