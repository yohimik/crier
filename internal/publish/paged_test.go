package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/yohimik/crier/internal/render"
)

func pages(n int) []render.Artifact {
	out := make([]render.Artifact, n)
	for i := range out {
		out[i] = render.Artifact{Path: fmt.Sprintf("/p%d.png", i+1), Kind: render.KindImage}
	}
	return out
}

func pageURLs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("https://staged/%d.png", i+1)
	}
	return out
}

func paths(arts []render.Artifact) []string {
	out := make([]string, len(arts))
	for i, a := range arts {
		out[i] = a.Path
	}
	return out
}

// TestBatchesCutsOnTheCap covers the arithmetic that is easy to get wrong: the
// last batch is short when the list does not divide evenly.
func TestBatchesCutsOnTheCap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		total    int
		capacity int
		want     [][]string
	}{
		{"fits", 3, 10, [][]string{{"/p1.png", "/p2.png", "/p3.png"}}},
		{"exactly full", 4, 4, [][]string{{"/p1.png", "/p2.png", "/p3.png", "/p4.png"}}},
		{"one over", 5, 4, [][]string{{"/p1.png", "/p2.png", "/p3.png", "/p4.png"}, {"/p5.png"}}},
		{"twelve at ten", 12, 10, [][]string{
			{"/p1.png", "/p2.png", "/p3.png", "/p4.png", "/p5.png", "/p6.png", "/p7.png", "/p8.png", "/p9.png", "/p10.png"},
			{"/p11.png", "/p12.png"},
		}},
		{"one at a time", 3, 1, [][]string{{"/p1.png"}, {"/p2.png"}, {"/p3.png"}}},
		{"a capacity below one still posts", 2, 0, [][]string{{"/p1.png"}, {"/p2.png"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Batches(pages(tc.total), nil, tc.capacity)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d batches, want %d", len(got), len(tc.want))
			}
			at := 0
			for i, b := range got {
				if b.First != at {
					t.Errorf("batch %d starts at page %d, want %d", i+1, b.First, at)
				}
				at += len(b.Artifacts)
				if got, want := paths(b.Artifacts), tc.want[i]; strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("batch %d = %v, want %v", i+1, got, want)
				}
			}
			if at != tc.total {
				t.Errorf("the batches cover %d pages, want all %d", at, tc.total)
			}
		})
	}
}

// TestBatchesKeepURLsAlignedWithFiles: a platform that fetches gets addresses
// for the files in the batch it was handed, not for the run's first ones.
func TestBatchesKeepURLsAlignedWithFiles(t *testing.T) {
	got := Batches(pages(5), pageURLs(5), 2)
	if len(got) != 3 {
		t.Fatalf("batches = %d", len(got))
	}
	if got[1].URLs[0] != "https://staged/3.png" || got[1].URLs[1] != "https://staged/4.png" {
		t.Errorf("batch 2 urls = %v, want the third and fourth pages", got[1].URLs)
	}
	for i, b := range got {
		if len(b.URLs) != len(b.Artifacts) {
			t.Errorf("batch %d has %d files and %d urls", i+1, len(b.Artifacts), len(b.URLs))
		}
	}

	// A run that staged nothing hands out no addresses rather than a short
	// list that would put a batch's files and urls out of step.
	for _, b := range Batches(pages(5), nil, 2) {
		if b.URLs != nil {
			t.Errorf("urls = %v, want none", b.URLs)
		}
	}
	for _, b := range Batches(pages(5), pageURLs(3), 2) {
		if b.URLs != nil {
			t.Errorf("a url list of the wrong length should be ignored, got %v", b.URLs)
		}
	}
}

func TestBatchesOfNothing(t *testing.T) {
	if got := Batches(nil, nil, 4); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestCapacityReadsZeroAsOne(t *testing.T) {
	for in, want := range map[int]int{-3: 1, 0: 1, 1: 1, 4: 4, 10: 10} {
		if got := (Needs{MaxAttachments: in}).Capacity(); got != want {
			t.Errorf("Capacity(%d) = %d, want %d", in, got, want)
		}
	}
}

// recordingPublisher remembers the order it was called in, and how many calls
// were in flight at once.
type recordingPublisher struct {
	name string
	mu   sync.Mutex
	// seen is the first file of every post it received, in order.
	seen []string
	// overlap is how many posts were ever in flight at the same time.
	inFlight, overlap int
	fail              map[int]error
	started           chan struct{}
	release           chan struct{}
}

func (r *recordingPublisher) Name() string { return r.name }
func (r *recordingPublisher) Needs() Needs { return Needs{} }
func (r *recordingPublisher) Ping(context.Context) (Identity, error) {
	return Identity{ID: r.name}, nil
}

func (r *recordingPublisher) Publish(_ context.Context, in Input) (Result, error) {
	r.mu.Lock()
	r.inFlight++
	if r.inFlight > r.overlap {
		r.overlap = r.inFlight
	}
	n := len(r.seen)
	r.seen = append(r.seen, in.Artifact.Path)
	r.mu.Unlock()

	if r.started != nil {
		r.started <- struct{}{}
		<-r.release
	}

	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()

	if err, bad := r.fail[n]; bad {
		return Result{}, err
	}
	return Result{ID: fmt.Sprintf("%s-%d", r.name, n+1)}, nil
}

func postsOf(arts []render.Artifact) []Input {
	out := make([]Input, len(arts))
	for i, a := range arts {
		out[i] = Input{Artifact: a, Artifacts: []render.Artifact{a}, Post: i + 1, Posts: len(arts)}
	}
	return out
}

// TestPostsGoOutInOrderOneAtATime is the ordering guarantee. Several platforms
// order a feed by when a post completed, so publishing two of one sequence at
// once is how a two-part post turns up back to front.
func TestPostsGoOutInOrderOneAtATime(t *testing.T) {
	p := &recordingPublisher{name: "seq"}
	rep := RunAll(context.Background(), []Job{{Publisher: p, Posts: postsOf(pages(5))}},
		8, testLogger(t))

	if rep.Failed() != 0 {
		t.Fatalf("report = %+v", rep.Outcomes)
	}
	want := []string{"/p1.png", "/p2.png", "/p3.png", "/p4.png", "/p5.png"}
	if strings.Join(p.seen, ",") != strings.Join(want, ",") {
		t.Errorf("posted %v, want %v", p.seen, want)
	}
	if p.overlap != 1 {
		t.Errorf("%d posts were in flight at once; a sequence has to go one at a time", p.overlap)
	}
	out := rep.Outcomes[0]
	if len(out.Posts) != 5 || out.Published() != 5 {
		t.Errorf("outcome = %+v", out)
	}
	if out.ID != "seq-1" {
		t.Errorf("the platform's line should name the first post, got %q", out.ID)
	}
}

// TestAFailedPostStopsTheRest: a gap in the middle of a sequence is worse than
// a short sequence, because a reader cannot tell it happened.
func TestAFailedPostStopsTheRest(t *testing.T) {
	p := &recordingPublisher{name: "seq", fail: map[int]error{3: errors.New("rate limited")}}
	rep := RunAll(context.Background(), []Job{{Publisher: p, Posts: postsOf(pages(5))}},
		1, testLogger(t))

	if len(p.seen) != 4 {
		t.Fatalf("it sent %d posts, want it to stop at the failure: %v", len(p.seen), p.seen)
	}
	out := rep.Outcomes[0]
	if out.OK {
		t.Error("the platform did not take the whole sequence; that is a failure")
	}
	if out.Published() != 3 {
		t.Errorf("published = %d, want the three that landed", out.Published())
	}
	for _, want := range []string{"posts 1 to 3 of 5 went out", "post 4 failed", "rate limited"} {
		if !strings.Contains(out.Error, want) {
			t.Errorf("error does not say %q: %s", want, out.Error)
		}
	}
	if len(out.Posts) != 4 || out.Posts[3].OK {
		t.Errorf("per-post breakdown = %+v", out.Posts)
	}
}

// TestAFirstPostFailureSaysNothingWentOut, because "posts 1 to 0" would be a
// strange thing to read.
func TestAFirstPostFailureSaysNothingWentOut(t *testing.T) {
	p := &recordingPublisher{name: "seq", fail: map[int]error{0: errors.New("token expired")}}
	rep := RunAll(context.Background(), []Job{{Publisher: p, Posts: postsOf(pages(3))}},
		1, testLogger(t))
	out := rep.Outcomes[0]
	if !strings.Contains(out.Error, "post 1 of 3 failed and none went out") {
		t.Errorf("error = %s", out.Error)
	}
	if out.Published() != 0 {
		t.Errorf("published = %d", out.Published())
	}
}

// TestOnePostReportsNoBreakdown: an ordinary post has nothing a per-post
// breakdown would add to the platform's own line.
func TestOnePostReportsNoBreakdown(t *testing.T) {
	p := &recordingPublisher{name: "one"}
	rep := RunAll(context.Background(), []Job{{Publisher: p, Posts: postsOf(pages(1))}},
		1, testLogger(t))
	out := rep.Outcomes[0]
	if len(out.Posts) != 0 {
		t.Errorf("posts = %+v", out.Posts)
	}
	if !out.OK || out.Published() != 1 {
		t.Errorf("outcome = %+v", out)
	}
}

// TestPlatformsStillRunTogether: sequencing is within one platform. Two
// platforms that each have a sequence still run alongside each other, which is
// the whole point of posting to eight places.
func TestPlatformsStillRunTogether(t *testing.T) {
	a := &recordingPublisher{name: "a", started: make(chan struct{}), release: make(chan struct{})}
	b := &recordingPublisher{name: "b", started: make(chan struct{}), release: make(chan struct{})}

	done := make(chan Report, 1)
	go func() {
		done <- RunAll(context.Background(), []Job{
			{Publisher: a, Posts: postsOf(pages(2))},
			{Publisher: b, Posts: postsOf(pages(2))},
		}, 2, testLogger(t))
	}()

	// Both platforms reach their first post before either is let go, which
	// they could not do if the runner serialised across platforms.
	<-a.started
	<-b.started
	a.release <- struct{}{}
	b.release <- struct{}{}
	<-a.started
	<-b.started
	a.release <- struct{}{}
	b.release <- struct{}{}

	rep := <-done
	if rep.Failed() != 0 {
		t.Errorf("report = %+v", rep.Outcomes)
	}
}

// TestASequenceStopsOnACancelledRun rather than sending the rest into a
// context that is already done.
func TestASequenceStopsOnACancelledRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &recordingPublisher{name: "seq"}
	rep := RunAll(ctx, []Job{{Publisher: p, Posts: postsOf(pages(4))}}, 1, testLogger(t))
	if len(p.seen) != 0 {
		t.Errorf("it sent %v on a cancelled run", p.seen)
	}
	if rep.Outcomes[0].OK {
		t.Error("a cancelled run is not a success")
	}
}

// TestInputSequenceReadsBothShapes: a publisher written before carousels reads
// Artifact and gets the first file; one that takes several reads Sequence and
// gets a one-entry list on an ordinary post.
func TestInputSequenceReadsBothShapes(t *testing.T) {
	single := Input{Artifact: render.Artifact{Path: "/a.png"}, URL: "https://a"}
	if got := paths(single.Sequence()); len(got) != 1 || got[0] != "/a.png" {
		t.Errorf("sequence = %v", got)
	}
	if got := single.SequenceURLs(); len(got) != 1 || got[0] != "https://a" {
		t.Errorf("urls = %v", got)
	}
	if single.Files() != 1 {
		t.Errorf("files = %d", single.Files())
	}

	many := Input{
		Artifact:  render.Artifact{Path: "/a.png"},
		Artifacts: pages(3),
		URLs:      pageURLs(3),
	}
	if many.Files() != 3 || len(many.Sequence()) != 3 || len(many.SequenceURLs()) != 3 {
		t.Errorf("input = %+v", many)
	}

	// A post with neither a URL nor a URL list has no addresses at all, rather
	// than one empty string a publisher would send as a real value.
	bare := Input{Artifact: render.Artifact{Path: "/a.png"}}
	if got := bare.SequenceURLs(); got != nil {
		t.Errorf("urls = %v, want none", got)
	}
}
