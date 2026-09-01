package publish

import (
	"fmt"
	"io"
	"os"
)

// Chunk is one slice of a file, with inclusive byte offsets — the form a
// Content-Range header wants.
type Chunk struct {
	Index int
	Start int64
	End   int64 // inclusive
	Size  int64
}

// ContentRange renders the chunk as the header value the chunked upload APIs
// expect: "bytes 0-5242879/10485760".
func (c Chunk) ContentRange(total int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", c.Start, c.End, total)
}

// SplitChunks divides a file of total bytes into chunks of at most size.
//
// The three chunked APIs crier talks to — TikTok, X and LinkedIn — differ in
// their limits and in how they name the pieces, and agree on the arithmetic.
// Getting the last chunk's length wrong is the classic way to have an upload
// accepted and then fail to process, so it is one function with its own tests.
func SplitChunks(total, size int64) []Chunk {
	if total <= 0 || size <= 0 {
		return nil
	}
	var out []Chunk
	for start := int64(0); start < total; start += size {
		end := start + size - 1
		if end >= total {
			end = total - 1
		}
		out = append(out, Chunk{
			Index: len(out),
			Start: start,
			End:   end,
			Size:  end - start + 1,
		})
	}
	return out
}

// TikTokChunks divides a file the way TikTok's video upload wants it.
//
// TikTok takes chunks between 5MB and 64MB, and refuses a final chunk smaller
// than 5MB — so a file that is not a clean multiple is uploaded as one chunk
// when it is small enough, and otherwise with the remainder folded into the
// last full chunk.
func TikTokChunks(total int64) (chunkSize int64, chunks []Chunk) {
	const (
		minChunk = 5 << 20
		maxChunk = 64 << 20
	)
	if total <= 0 {
		return 0, nil
	}
	if total <= minChunk {
		// One chunk covering the whole file; TikTok documents this as the way
		// to send anything under the minimum.
		return total, []Chunk{{Index: 0, Start: 0, End: total - 1, Size: total}}
	}
	chunkSize = minChunk
	if total/chunkSize > 1000 {
		chunkSize = maxChunk
	}
	count := total / chunkSize
	chunks = make([]Chunk, 0, count)
	for i := int64(0); i < count; i++ {
		start := i * chunkSize
		end := start + chunkSize - 1
		if i == count-1 {
			// The remainder rides along with the last chunk rather than
			// becoming a short one TikTok would reject.
			end = total - 1
		}
		chunks = append(chunks, Chunk{Index: int(i), Start: start, End: end, Size: end - start + 1})
	}
	return chunkSize, chunks
}

// readChunk reads one chunk out of a file.
func readChunk(path string, c Chunk) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, c.Size)
	if _, err := f.ReadAt(buf, c.Start); err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading %s at %d: %w", path, c.Start, err)
	}
	return buf, nil
}
