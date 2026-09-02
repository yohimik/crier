package publish

import "github.com/yohimik/crier/internal/render"

// Batch is one post's worth of a page list: which pages it carries and where
// they sit in the run.
type Batch struct {
	// First is the index of this batch's first page in the run's page list,
	// counting from zero.
	First int
	// Artifacts are the files, in page order.
	Artifacts []render.Artifact
	// URLs are where those files can be fetched, in the same order. It is nil
	// for a platform that takes bytes.
	URLs []string
}

// Batches cuts one ordered page list into the posts a platform will take.
//
// Every platform receives the same pages in the same order. What differs is
// how many fit in one post: ten at Instagram, four at X, one wherever a post is
// one picture. A list longer than the cap becomes several posts in a row
// rather than a truncated one, because dropping the tail of a changelog is a
// worse answer than posting it in two parts.
//
// The last batch is short when the list does not divide evenly, which is the
// arithmetic worth having a test for.
func Batches(arts []render.Artifact, urls []string, capacity int) []Batch {
	if len(arts) == 0 {
		return nil
	}
	if capacity < 1 {
		capacity = 1
	}
	out := make([]Batch, 0, (len(arts)+capacity-1)/capacity)
	for start := 0; start < len(arts); start += capacity {
		end := start + capacity
		if end > len(arts) {
			end = len(arts)
		}
		b := Batch{First: start, Artifacts: arts[start:end]}
		// The URL list is either empty or exactly as long as the page list, so
		// a platform that fetches never gets a batch whose addresses are off by
		// one from its files.
		if len(urls) == len(arts) {
			b.URLs = urls[start:end]
		}
		out = append(out, b)
	}
	return out
}
