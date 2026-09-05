package app

import (
	"context"

	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/stage"
)

// coverStoryPublisher keeps the optional story inside publish.RunAll. Its
// encode, stage and Instagram calls can fail without preventing independent
// feed jobs from finishing, while the shared report still returns nonzero.
type coverStoryPublisher struct {
	instagram publish.Publisher
	pipeline  *Pipeline
	stager    stage.Stager
}

func (p *coverStoryPublisher) Name() string         { return "instagram-cover-story" }
func (p *coverStoryPublisher) Needs() publish.Needs { return p.instagram.Needs() }
func (p *coverStoryPublisher) Ping(ctx context.Context) (publish.Identity, error) {
	return p.instagram.Ping(ctx)
}

func (p *coverStoryPublisher) Publish(ctx context.Context, in publish.Input) (publish.Result, error) {
	story, err := p.pipeline.CoverStory(ctx, in.Artifact)
	if err != nil {
		return publish.Result{}, err
	}
	if err := p.pipeline.Stage(ctx, p.stager, &story, true, false); err != nil {
		return publish.Result{}, err
	}
	video := *story.Video
	return p.instagram.Publish(ctx, publish.Input{
		Artifact: video,
		URL:      story.URL(), URLs: story.URLs(),
		Post: 1, Posts: 1, Page: 1, Pages: 1,
	})
}
