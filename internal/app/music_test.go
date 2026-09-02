package app

import (
	"strings"
	"testing"
)

// jingleBytes is an mp3 header and nothing more. Nothing here decodes audio;
// `crier ping` reads the first bytes and stops.
const jingleBytes = "ID3\x04\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

// telegramBlock is a telegram that is configured but points nowhere, so the
// platform row fails fast and the music row is the one under test.
var telegramBlock = []string{
	"http:",
	"  retry-max: 0",
	"  timeout: 2s",
	"publish:",
	"  telegram:",
	"    enabled: true",
	"    api-base-url: http://127.0.0.1:1",
	"    token: t",
	"    chat-id: c",
}

// TestPingChecksTheMusicFile is the whole of the audio check: the file is
// there, it can be read, and its first bytes are one of the four containers.
func TestPingChecksTheMusicFile(t *testing.T) {
	dir := project(t, strings.Join(append(append([]string(nil), telegramBlock...),
		"  music-file: jingle.mp3"), "\n"))
	write(t, dir, "jingle.mp3", jingleBytes)

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "music") {
		t.Fatalf("no music row:\n%s", stdout)
	}
	for _, want := range []string{"jingle.mp3", "mp3", "telegram"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
}

func TestPingReportsAMusicFileThatIsNotThere(t *testing.T) {
	dir := project(t, strings.Join(append(append([]string(nil), telegramBlock...),
		"  music-file: nowhere.mp3"), "\n"))

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "music") || !strings.Contains(stdout, "failed") {
		t.Fatalf("the missing file was not reported:\n%s", stdout)
	}
}

// TestPingReportsAMusicFileThatIsNotAudio: an image renamed to .mp3 would be
// accepted by the file system and refused by the platform, after the post.
func TestPingReportsAMusicFileThatIsNotAudio(t *testing.T) {
	dir := project(t, strings.Join(append(append([]string(nil), telegramBlock...),
		"  music-file: jingle.mp3"), "\n"))
	write(t, dir, "jingle.mp3", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "does not begin like an audio file") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

// TestPingNamesThePlatformThatOverrodeTheFile: one row per key, so an
// override that is broken says which line to change.
func TestPingNamesThePlatformThatOverrodeTheFile(t *testing.T) {
	dir := project(t, strings.Join(append(append([]string(nil), telegramBlock...),
		"    music-file: telegram.mp3",
		"  music-file: shared.mp3"), "\n"))
	write(t, dir, "shared.mp3", jingleBytes)
	write(t, dir, "telegram.mp3", jingleBytes)

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "music:telegram") {
		t.Fatalf("no per-platform row:\n%s", stdout)
	}
	// Telegram takes its own file, so the shared one now reaches nothing.
	if !strings.Contains(stdout, "no enabled platform can carry it") {
		t.Errorf("the shared file's note is wrong:\n%s", stdout)
	}
}

// TestPingSaysWhenNothingCanCarryTheMusic: the file is fine, the run around it
// is not, and only the operator can say which was meant.
func TestPingSaysWhenNothingCanCarryTheMusic(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"http:",
		"  retry-max: 0",
		"  timeout: 2s",
		"publish:",
		"  music-file: jingle.mp3",
		"  x:",
		"    enabled: true",
		"    api-base-url: http://127.0.0.1:1",
		"    token: t",
	}, "\n"))
	write(t, dir, "jingle.mp3", jingleBytes)

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "no enabled platform can carry it") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

// TestPingHasNoMusicRowWhenNoneIsConfigured: a target that is always there
// would be a row saying nothing, on every run anybody ever makes.
func TestPingHasNoMusicRowWhenNoneIsConfigured(t *testing.T) {
	dir := project(t, strings.Join(telegramBlock, "\n"))
	_, stdout, _ := run(t, dir, []string{}, "ping")
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "music") {
			t.Errorf("an unasked-for music row:\n%s", stdout)
		}
	}
}

// TestPublishWarnsWhenNothingCanCarryTheMusic is the same finding at the other
// command: a dry run makes no requests and still says the track goes nowhere.
func TestPublishWarnsWhenNothingCanCarryTheMusic(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  dry-run: true",
		"  music-file: jingle.mp3",
		"  x:",
		"    enabled: true",
		"    token: t",
	}, "\n"))
	write(t, dir, "jingle.mp3", jingleBytes)

	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "no enabled platform can attach an audio file") {
		t.Errorf("no warning:\n%s", stderr)
	}
}

// TestPublishIsSilentWhenTheMusicHasSomewhereToGo guards the other side of it:
// a warning on every ordinary run would teach people to ignore warnings.
func TestPublishIsSilentWhenTheMusicHasSomewhereToGo(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  dry-run: true",
		"  music-file: jingle.mp3",
		"  discord:",
		"    enabled: true",
		"    webhook-url: https://discord.example/webhook",
	}, "\n"))
	write(t, dir, "jingle.mp3", jingleBytes)

	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
	if strings.Contains(stderr, "no enabled platform can attach an audio file") {
		t.Errorf("warned about a file discord will carry:\n%s", stderr)
	}
}

// TestPublishRefusesAMusicFileThatIsNotAudio: the check happens where a
// missing token is checked, which is before anything is rendered.
func TestPublishRefusesAMusicFileThatIsNotAudio(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  dry-run: true",
		"  music-file: jingle.mp3",
		"  discord:",
		"    enabled: true",
		"    webhook-url: https://discord.example/webhook",
	}, "\n"))
	write(t, dir, "jingle.mp3", "not audio at all")

	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitConfig {
		t.Fatalf("code = %d, want a config error; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "publish.music-file") {
		t.Errorf("the error does not name the key:\n%s", stderr)
	}
}

// TestMusicOnAPlatformThatCannotCarryItIsRefused is the validation error, seen
// from the command line where an operator meets it.
func TestMusicOnAPlatformThatCannotCarryItIsRefused(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  dry-run: true",
		"  instagram:",
		"    enabled: true",
		"    token: t",
		"    user-id: u",
		"    music-file: jingle.mp3",
	}, "\n"))
	write(t, dir, "jingle.mp3", jingleBytes)

	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitConfig {
		t.Fatalf("code = %d, want a config error; stderr = %s", code, stderr)
	}
	for _, want := range []string{"publish.instagram.music-file", "discord, slack, telegram"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
}
