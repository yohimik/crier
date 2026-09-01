package publish

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// TestInstagramReportsNoLinkRatherThanABadOne: the post exists either way, and
// a link that goes nowhere is worse than none.
func TestInstagramReportsNoLinkRatherThanABadOne(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		default:
			// The permalink lookup fails.
			w.WriteHeader(http.StatusForbidden)
		}
	})

	res, err := onlyPublisher(t, instagramConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	})
	if err != nil {
		t.Fatalf("a permalink lookup failing must not fail the post: %v", err)
	}
	if res.ID != "p1" {
		t.Errorf("result = %+v", res)
	}
	if res.URL != "" {
		t.Errorf("url = %q, want none rather than one that goes nowhere", res.URL)
	}
}

// TestCustomPlatformTakesGIFs is the regression test for a silent drop: an
// unrecognised kind word vanished from Needs, so any enabled custom platform
// blocked every render.video.format=gif run with no way to opt in.
func TestCustomPlatformTakesGIFs(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Custom = map[string]*config.Custom{"hook": {
		Enabled: true, Command: "true", Kinds: []string{"gif"}, Format: "png", Timeout: "10s",
	}}
	if err := config.Validate(&cfg); err != nil {
		t.Fatalf("gif is a kind a custom platform may take: %v", err)
	}

	needs := onlyPublisher(t, &cfg).Needs()
	if !needs.Accepts(render.KindGIF) {
		t.Fatalf("kinds = %v, want the gif to have survived", needs.Kinds)
	}
	if needs.Accepts(render.KindVideo) {
		t.Errorf("kinds = %v: only what was asked for", needs.Kinds)
	}

	// All three together.
	cfg.Publish.Custom["hook"].Kinds = []string{"image", "video", "gif"}
	needs = onlyPublisher(t, &cfg).Needs()
	for _, kind := range []render.Kind{render.KindImage, render.KindVideo, render.KindGIF} {
		if !needs.Accepts(kind) {
			t.Errorf("%s was dropped: %v", kind, needs.Kinds)
		}
	}
}

// TestCustomPlatformRefusesAnUnknownKind: the word is rejected rather than
// dropped, so a typo is a message instead of a platform that quietly refuses
// everything.
func TestCustomPlatformRefusesAnUnknownKind(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Custom = map[string]*config.Custom{"hook": {
		Enabled: true, Command: "true", Kinds: []string{"bogus"}, Format: "png", Timeout: "10s",
	}}
	err := config.Validate(&cfg)
	if err == nil {
		t.Fatal("an unknown kind should be a config error")
	}
	if !strings.Contains(err.Error(), "kinds") || !strings.Contains(err.Error(), "gif") {
		t.Errorf("the error should name the key and the valid words: %v", err)
	}
}

// TestTikTokRefusesAnOversizedVideo: TikTok caps a video at 4GB, and finding
// that out after uploading it is the slowest possible way to be told.
func TestTikTokRefusesAnOversizedVideo(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("nothing should have been uploaded")
		w.WriteHeader(http.StatusInternalServerError)
	})
	big := videoArtifact(t, 16)
	big.Size = TikTokVideoLimit + 1

	_, err := onlyPublisher(t, tiktokConfig(srv.URL)).Publish(context.Background(), Input{Artifact: big})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
	if !strings.Contains(err.Error(), "4.0GB") {
		t.Errorf("the message should name TikTok's limit: %v", err)
	}
}

// TestTikTokChunksStayWithinTheDocumentedBounds.
//
// TikTok's media transfer guide: 5MB to 64MB per chunk, at most 1000 chunks,
// and the last chunk may run past chunk_size — but only to 128MB — to absorb
// the trailing bytes. total_chunk_count is video_size divided by chunk_size,
// rounded down.
func TestTikTokChunksStayWithinTheDocumentedBounds(t *testing.T) {
	const (
		minChunk = 5 << 20
		maxChunk = 64 << 20
	)
	for _, total := range []int64{
		1, minChunk - 1, minChunk, minChunk + 1,
		10 << 20, 64 << 20, 100 << 20,
		1 << 30, TikTokVideoLimit,
		// Past the 4GB cap, so it only reaches here through the chunker: the
		// branch that switches to 64MB chunks lives at 5GB and the size check
		// refuses the upload first, but the arithmetic still has to hold.
		6 << 30,
	} {
		size, chunks := TikTokChunks(total)
		if len(chunks) == 0 {
			t.Errorf("%d bytes produced no chunks", total)
			continue
		}
		if len(chunks) > 1000 {
			t.Errorf("%d bytes produced %d chunks, over the 1000 maximum", total, len(chunks))
		}
		if total > minChunk && (size < minChunk || size > maxChunk) {
			t.Errorf("%d bytes chose a %d chunk size, outside 5MB..64MB", total, size)
		}
		if int64(len(chunks)) != max64(1, total/size) {
			t.Errorf("%d bytes at %d gave %d chunks, want video_size/chunk_size rounded down",
				total, size, len(chunks))
		}

		var covered int64
		for i, c := range chunks {
			if c.Size <= 0 {
				t.Errorf("%d bytes: chunk %d is empty", total, i)
			}
			last := i == len(chunks)-1
			switch {
			case last && c.Size > TikTokFinalChunkLimit:
				t.Errorf("%d bytes: the last chunk is %d, over the 128MB the guide allows", total, c.Size)
			case !last && c.Size != size:
				t.Errorf("%d bytes: chunk %d is %d, want the declared %d", total, i, c.Size, size)
			}
			if c.Start != covered {
				t.Errorf("%d bytes: chunk %d starts at %d, want %d", total, i, c.Start, covered)
			}
			covered = c.End + 1
		}
		if covered != total {
			t.Errorf("%d bytes: the chunks cover %d", total, covered)
		}
	}

	if size, chunks := TikTokChunks(0); size != 0 || chunks != nil {
		t.Errorf("an empty file = %d, %v", size, chunks)
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
