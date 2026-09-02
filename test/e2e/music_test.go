//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// jingle is a file that begins like an mp3 and then says something a test can
// look for. crier reads the first bytes and never decodes the rest, so the
// marker rides along to every platform untouched.
const jingle = "ID3\x04\x00\x00\x00\x00\x00\x00CRIER-JINGLE-BYTES"

// jingleMarker is the part of it an assertion looks for in a request body.
const jingleMarker = "CRIER-JINGLE-BYTES"

// enableCarriers turns on the three platforms whose API can carry audio.
func enableCarriers(f *fakes) string {
	return strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
		"  slack:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/slack",
		"    token: xoxb-e2e",
		"    channel: C-E2E",
	}, "\n")
}

// TestMusicReachesTheThreePlatformsThatCanCarryIt is the feature end to end:
// one audio file named once, and the bytes arrive at each of the three in the
// shape that platform's API wants.
func TestMusicReachesTheThreePlatformsThatCanCarryIt(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableCarriers(f)+"\n  music-file: jingle.mp3\n")
	writeFile(t, dir, "jingle.mp3", jingle)

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	// Discord: one message, with the audio as another files[n] part.
	dc, ok := f.find("/discord/webhook")
	if !ok {
		t.Fatal("discord received nothing")
	}
	if !strings.Contains(dc.Body, jingleMarker) {
		t.Error("the audio bytes did not reach discord")
	}
	if !strings.Contains(dc.Body, `name="files[1]"`) {
		t.Errorf("the audio is not a second file part: %q", dc.Body)
	}
	if n := f.count("/discord/webhook"); n != 1 {
		t.Errorf("discord got %d messages, want the audio in the same one", n)
	}

	// Slack: the picture and the audio each get a slot, and one call shares
	// them together, which is what makes them one message.
	if n := f.count("/slack/files.getUploadURLExternal"); n != 2 {
		t.Errorf("slack handed out %d upload slots, want 2", n)
	}
	if n := f.count("/slack/files.completeUploadExternal"); n != 1 {
		t.Errorf("slack shared in %d calls, want 1", n)
	}
	uploads := f.findAll("/slack-upload")
	if len(uploads) != 2 {
		t.Fatalf("slack received %d uploads, want 2", len(uploads))
	}
	if !strings.Contains(uploads[0].Body+uploads[1].Body, jingleMarker) {
		t.Error("the audio bytes did not reach slack")
	}

	// Telegram: a second message, and strictly after the post.
	audio, ok := f.find("/sendAudio")
	if !ok {
		t.Fatal("telegram received no audio message")
	}
	if !strings.Contains(audio.Body, jingleMarker) {
		t.Error("the audio bytes did not reach telegram")
	}
	if !strings.Contains(audio.Body, `name="audio"`) {
		t.Errorf("the audio part is not named audio: %q", audio.Body)
	}
	if !strings.Contains(audio.Body, "@crier") {
		t.Errorf("the audio went to no chat: %q", audio.Body)
	}
	if got := indexOfPath(f, "/sendPhoto"); got < 0 || got > indexOfPath(f, "/sendAudio") {
		t.Errorf("the audio must arrive after the post: %v", pathsOf(f))
	}
}

// TestMusicFollowsTheAlbum is the ordering rule where it actually matters: a
// paginated run posts an album, and the track has to land under it.
func TestMusicFollowsTheAlbum(t *testing.T) {
	f := newFakes(t)
	// A template that lays out into five pages, so telegram sends an album
	// rather than one photo.
	dir := newPagedProject(t, enableCarriers(f)+"\n  music-file: jingle.mp3\n")
	writeFile(t, dir, "jingle.mp3", jingle)

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	album := indexOfPath(f, "/sendMediaGroup")
	audio := indexOfPath(f, "/sendAudio")
	if album < 0 {
		t.Fatalf("no media group was sent: %v", pathsOf(f))
	}
	if audio < 0 || audio < album {
		t.Errorf("the audio must follow the album: %v", pathsOf(f))
	}
}

// TestPingChecksTheMusicFile: the audio gets a row of its own, naming the
// platforms it will reach.
func TestPingChecksTheMusicFile(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableCarriers(f)+"\n  music-file: jingle.mp3\n")
	writeFile(t, dir, "jingle.mp3", jingle)

	res := crier(t, dir, nil, "ping")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "music") || !strings.Contains(res.Stdout, "jingle.mp3") {
		t.Fatalf("no music row:\n%s", res.Stdout)
	}
	for _, want := range []string{"mp3", "discord", "slack", "telegram"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("missing %q in:\n%s", want, res.Stdout)
		}
	}
	// Nothing was posted: every request ping made was a read.
	if _, ok := f.find("/sendAudio"); ok {
		t.Error("ping sent the audio")
	}
}

// TestPingRefusesAMusicFileThatIsNotAudio: the file is checked before the
// publishers are built, so the row says which file and why.
func TestPingRefusesAMusicFileThatIsNotAudio(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableCarriers(f)+"\n  music-file: jingle.mp3\n")
	writeFile(t, dir, "jingle.mp3", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

	res := crier(t, dir, nil, "ping")
	if res.Code != exitConfig {
		t.Fatalf("code = %d, want a config error; stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "does not begin like an audio file") {
		t.Errorf("stdout:\n%s", res.Stdout)
	}
}

// TestPingReportsAMusicFileThatIsNotThere is the other half: the path is a
// path key, so a name in the config file means the file beside it.
func TestPingReportsAMusicFileThatIsNotThere(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableCarriers(f)+"\n  music-file: nowhere.mp3\n")

	res := crier(t, dir, nil, "ping")
	if res.Code != exitConfig {
		t.Fatalf("code = %d, want a config error; stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "music") || !strings.Contains(res.Stdout, "failed") {
		t.Errorf("stdout:\n%s", res.Stdout)
	}
}

// TestMusicOnAPlatformThatCannotCarryItIsRefused is the validation error as an
// operator meets it: named platform, named reason, nothing rendered.
func TestMusicOnAPlatformThatCannotCarryItIsRefused(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableCarriers(f)+
		"\n  x:\n    enabled: true\n    api-base-url: "+f.URL+"/x\n    token: x-token\n"+
		"    music-file: jingle.mp3\n")
	writeFile(t, dir, "jingle.mp3", jingle)

	res := crier(t, dir, nil, "publish")
	if res.Code != exitConfig {
		t.Fatalf("code = %d, want a config error; stderr=%s", res.Code, res.Stderr)
	}
	for _, want := range []string{"publish.x.music-file", "discord, slack, telegram"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("missing %q in:\n%s", want, res.Stderr)
		}
	}
	if len(f.all()) != 0 {
		t.Errorf("a refused configuration made %d requests", len(f.all()))
	}
}

// TestMusicWithNoCarrierEnabledWarns: the run is fine and the track goes
// nowhere, which is worth saying out loud.
func TestMusicWithNoCarrierEnabledWarns(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  x:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/x",
		"    token: x-token",
		"  music-file: jingle.mp3",
	}, "\n"))
	writeFile(t, dir, "jingle.mp3", jingle)

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no enabled platform can attach an audio file") {
		t.Errorf("no warning:\n%s", res.Stderr)
	}
	if _, ok := f.find("/sendAudio"); ok {
		t.Error("something sent the audio")
	}
}

// anthemMP4 begins like an MP4 and then says something a test can look for.
// crier reads the first twelve bytes and never decodes the rest, so no ffmpeg
// is needed to make one.
const anthemMP4 = "\x00\x00\x00\x20ftypisomCRIER-ANTHEM-BYTES"

// TestLeadVideoOpensTheTelegramAlbum is the clip end to end at the platform
// that takes the bytes: the album opens with it and the track still follows.
func TestLeadVideoOpensTheTelegramAlbum(t *testing.T) {
	f := newFakes(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"    lead-video: anthem.mp4",
		"  music-file: jingle.mp3",
	}, "\n"))
	writeFile(t, dir, "anthem.mp4", anthemMP4)
	writeFile(t, dir, "jingle.mp3", jingle)

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	album, ok := f.find("/sendMediaGroup")
	if !ok {
		t.Fatal("no album was sent")
	}
	if !strings.Contains(album.Body, `"type":"video"`) {
		t.Errorf("the album carries no video: %q", album.Body)
	}
	// The clip is the first entry: everything before the first photo entry in
	// the media array belongs to it.
	video := strings.Index(album.Body, `"type":"video"`)
	photo := strings.Index(album.Body, `"type":"photo"`)
	if video < 0 || photo < 0 || video > photo {
		t.Errorf("the album does not open with the clip: %q", album.Body)
	}
	if !strings.Contains(album.Body, "CRIER-ANTHEM-BYTES") {
		t.Error("the clip's bytes did not go out")
	}
	if !strings.Contains(album.Body, `name="lead"`) {
		t.Errorf("the clip is not attached as a part: %q", album.Body)
	}

	// The audio is still its own message, after the album.
	if got := indexOfPath(f, "/sendAudio"); got < 0 || got < indexOfPath(f, "/sendMediaGroup") {
		t.Errorf("the track must follow the album: %v", pathsOf(f))
	}
}

// TestLeadVideoIsRefusedWhereItCannotWork: eleven of the thirteen post pictures or a
// video and never both, and the error says which and why.
func TestLeadVideoIsRefusedWhereItCannotWork(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
		"    lead-video: anthem.mp4",
	}, "\n"))
	writeFile(t, dir, "anthem.mp4", anthemMP4)

	res := crier(t, dir, nil, "publish")
	if res.Code != exitConfig {
		t.Fatalf("code = %d, want a config error; stderr=%s", res.Code, res.Stderr)
	}
	for _, want := range []string{"publish.discord.lead-video", "instagram and telegram"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("missing %q in:\n%s", want, res.Stderr)
		}
	}
	if len(f.all()) != 0 {
		t.Errorf("a refused configuration made %d requests", len(f.all()))
	}
}

// TestPingChecksTheLeadVideo: the clip gets its own row, like the track does.
func TestPingChecksTheLeadVideo(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"    lead-video: anthem.mp4",
	}, "\n"))
	writeFile(t, dir, "anthem.mp4", anthemMP4)

	res := crier(t, dir, nil, "ping")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	for _, want := range []string{"lead-video:telegram", "anthem.mp4", "mp4", "opens the telegram post"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("missing %q in:\n%s", want, res.Stdout)
		}
	}
	if _, ok := f.find("/sendMediaGroup"); ok {
		t.Error("ping posted the album")
	}
}

// indexOfPath is where the first request touching a fragment sits in the
// arrival order, or -1.
func indexOfPath(f *fakes, fragment string) int {
	for i, r := range f.all() {
		if strings.Contains(r.Path, fragment) {
			return i
		}
	}
	return -1
}

// pathsOf is every path the fakes saw, in order, for a failure message.
func pathsOf(f *fakes) []string {
	var out []string
	for _, r := range f.all() {
		out = append(out, r.Path)
	}
	return out
}
