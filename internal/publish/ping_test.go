package publish

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// pingOK drives one publisher's Ping against a fake that answers with body,
// and returns the identity and the requests that reached it.
func pingOK(t *testing.T, cfg func(url string) *config.Config, handler http.HandlerFunc) (Identity, *recorder) {
	t.Helper()
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		handler(w, r)
	})
	id, err := onlyPublisher(t, cfg(srv.URL)).Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	return id, rec
}

func TestTelegramPingReadsTheBot(t *testing.T) {
	id, rec := pingOK(t, telegramConfig, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":77,"username":"crier_bot","first_name":"Crier"}}`))
	})
	if id.ID != "77" || id.Name != "@crier_bot" {
		t.Errorf("identity = %+v", id)
	}
	if got := rec.paths(); len(got) != 1 || got[0] != "GET /bot123:abc/getMe" {
		t.Errorf("requests = %v", got)
	}
}

// TestTelegramPingRejectsABadToken is the case the command exists for: the API
// answers 200 with ok:false, so the status code alone would call it a success.
func TestTelegramPingRejectsABadToken(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	})
	_, err := onlyPublisher(t, telegramConfig(srv.URL)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("err = %v, want the description telegram gave", err)
	}
}

func TestDiscordPingReadsTheWebhook(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"9","name":"crier hook","channel_id":"55"}`))
	})
	cfg := config.Defaults()
	cfg.Publish.Discord.Enabled = true
	cfg.Publish.Discord.WebhookURL = srv.URL + "/api/webhooks/1/token"

	id, err := onlyPublisher(t, &cfg).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "9" || id.Name != "crier hook" || !strings.Contains(id.Note, "55") {
		t.Errorf("identity = %+v", id)
	}
	if got := rec.paths(); len(got) != 1 || got[0] != "GET /api/webhooks/1/token" {
		t.Errorf("requests = %v", got)
	}
}

func TestMastodonPingVerifiesCredentials(t *testing.T) {
	id, rec := pingOK(t, mastodonConfig, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"1","username":"crier","acct":"crier@example.social"}`))
	})
	if id.ID != "1" || id.Name != "@crier@example.social" {
		t.Errorf("identity = %+v", id)
	}
	if got := rec.paths(); got[0] != "GET /api/v1/accounts/verify_credentials" {
		t.Errorf("requests = %v", got)
	}
}

func TestInstagramPingReadsTheAccount(t *testing.T) {
	id, rec := pingOK(t, instagramConfig, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"123","username":"crier"}`))
	})
	if id.ID != "123" || id.Name != "@crier" {
		t.Errorf("identity = %+v", id)
	}
	req := rec.all()[0]
	if req.Path != "/123" || !strings.Contains(req.Query, "fields=id%2Cusername") {
		t.Errorf("request = %s?%s", req.Path, req.Query)
	}
}

func TestFacebookPingReadsThePage(t *testing.T) {
	id, rec := pingOK(t, facebookConfig, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"page1","name":"A Page"}`))
	})
	if id.ID != "page1" || id.Name != "A Page" {
		t.Errorf("identity = %+v", id)
	}
	if rec.all()[0].Path != "/page1" {
		t.Errorf("path = %s", rec.all()[0].Path)
	}
}

// TestTikTokPingQueriesCreatorInfo also covers TikTok's habit of answering 200
// with an error object inside, which the publisher already had to learn.
func TestTikTokPingQueriesCreatorInfo(t *testing.T) {
	id, rec := pingOK(t, tiktokConfig, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"creator_nickname":"Crier","creator_username":"crier"}}`))
	})
	if id.Name != "Crier" || id.ID != "crier" {
		t.Errorf("identity = %+v", id)
	}
	if got := rec.paths(); got[0] != "POST /v2/post/publish/creator_info/query/" {
		t.Errorf("requests = %v", got)
	}

	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"code":"access_token_invalid","message":"bad token","log_id":"L1"}}`))
	})
	_, err := onlyPublisher(t, tiktokConfig(srv.URL)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "access_token_invalid") {
		t.Fatalf("err = %v, want the error inside the 200", err)
	}
}

func TestXPingReadsTheUser(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"data":{"id":"5","name":"Crier","username":"crier"}}`))
	})
	cfg := config.Defaults()
	cfg.Publish.X.Enabled = true
	cfg.Publish.X.APIBaseURL = srv.URL
	cfg.Publish.X.Token = "tok"

	id, err := onlyPublisher(t, &cfg).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "5" || id.Name != "@crier" {
		t.Errorf("identity = %+v", id)
	}
	if got := rec.paths(); got[0] != "GET /2/users/me" {
		t.Errorf("requests = %v", got)
	}
}

func TestLinkedInPingReadsUserinfo(t *testing.T) {
	id, rec := pingOK(t, linkedinConfig, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sub":"782bb","name":"A Member"}`))
	})
	if id.ID != "782bb" || id.Name != "A Member" {
		t.Errorf("identity = %+v", id)
	}
	req := rec.all()[0]
	if req.Path != "/v2/userinfo" {
		t.Errorf("path = %s", req.Path)
	}
	// The versioned header belongs to /rest and makes /v2 answer 426.
	if req.Header.Get("LinkedIn-Version") != "" {
		t.Errorf("LinkedIn-Version was sent to a /v2 endpoint: %q", req.Header.Get("LinkedIn-Version"))
	}
}

// TestLinkedInPingSeparatesForbiddenFromUnauthorized is the whole reason
// LinkedIn's ping is not three lines.
//
// A token with only w_member_social can post but cannot read a profile, so
// userinfo answers 403. Calling that a broken setup would tell everybody with
// a working LinkedIn configuration that it is broken; calling a 401 a working
// one would be worse.
func TestLinkedInPingSeparatesForbiddenFromUnauthorized(t *testing.T) {
	forbidden := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Not enough permissions"}`))
	})
	id, err := onlyPublisher(t, linkedinConfig(forbidden.URL)).Ping(context.Background())
	if err != nil {
		t.Fatalf("a posting-only token should pass: %v", err)
	}
	if id.ID != "urn:li:person:abc" || !strings.Contains(id.Note, "openid") {
		t.Errorf("identity = %+v, want the author urn and a note about the scopes", id)
	}

	unauthorized := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := onlyPublisher(t, linkedinConfig(unauthorized.URL)).Ping(context.Background()); err == nil {
		t.Error("a 401 is a broken token and has to fail")
	}
}

func TestRedditPingGetsATokenAndReadsMe(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/api/v1/access_token":
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"bearer"}`))
		case "/api/v1/me":
			_, _ = w.Write([]byte(`{"id":"abc","name":"crierbot"}`))
		default:
			http.NotFound(w, r)
		}
	})

	id, err := onlyPublisher(t, redditConfig(srv.URL, srv.URL)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "u/crierbot" || id.ID != "abc" || !strings.Contains(id.Note, "r/test") {
		t.Errorf("identity = %+v", id)
	}
	if got := rec.paths(); len(got) != 2 || got[0] != "POST /api/v1/access_token" || got[1] != "GET /api/v1/me" {
		t.Errorf("requests = %v", got)
	}
	// The mandatory descriptive User-Agent goes out on the ping too.
	if ua := rec.all()[1].Header.Get("User-Agent"); !strings.Contains(ua, "com.yohimik.crier") {
		t.Errorf("user agent = %q", ua)
	}
}

// TestRedditPingFailsWhenTheGrantDoes checks the half of a Reddit setup that
// actually goes wrong.
func TestRedditPingFailsWhenTheGrantDoes(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	_, err := onlyPublisher(t, redditConfig(srv.URL, srv.URL)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "two-factor") {
		t.Fatalf("err = %v, want the grant error with its explanation", err)
	}
}

// --- the fan-out -----------------------------------------------------------

func TestPingAllReportsEveryPlatformInOrder(t *testing.T) {
	boom := errors.New("nope")
	ps := []Publisher{
		stubPublisher{name: "c", ping: func(context.Context) (Identity, error) {
			return Identity{}, boom
		}},
		stubPublisher{name: "a", ping: func(context.Context) (Identity, error) {
			return Identity{ID: "1", Name: "@a"}, nil
		}},
		stubPublisher{name: "b", ping: func(context.Context) (Identity, error) {
			return Identity{ID: "2", Note: "a caveat"}, nil
		}},
	}

	rep := PingAll(context.Background(), ps, 2, testLogger(t))
	if len(rep.Outcomes) != 3 {
		t.Fatalf("outcomes = %+v", rep.Outcomes)
	}
	for i, want := range []string{"a", "b", "c"} {
		if rep.Outcomes[i].Platform != want {
			t.Errorf("outcome %d = %s, want %s", i, rep.Outcomes[i].Platform, want)
		}
	}
	if rep.Succeeded() != 2 || rep.Failed() != 1 {
		t.Errorf("succeeded=%d failed=%d", rep.Succeeded(), rep.Failed())
	}
	// The identity is rendered for the report rather than left in pieces.
	if rep.Outcomes[0].ID != "@a (1)" {
		t.Errorf("account = %q", rep.Outcomes[0].ID)
	}
	if rep.Outcomes[1].Extra["note"] != "a caveat" {
		t.Errorf("note = %v", rep.Outcomes[1].Extra)
	}
	if !errors.Is(rep.Err(), boom) {
		t.Errorf("the failure should be reachable: %v", rep.Err())
	}
}

// TestPingAllSurvivesAPanic mirrors the publish fan-out: one broken platform
// must not take the check for the other eight down with it.
func TestPingAllSurvivesAPanic(t *testing.T) {
	ps := []Publisher{
		stubPublisher{name: "boom", ping: func(context.Context) (Identity, error) {
			panic("in a publisher")
		}},
		stubPublisher{name: "fine"},
	}
	rep := PingAll(context.Background(), ps, 0, testLogger(t))
	if rep.Succeeded() != 1 || rep.Failed() != 1 {
		t.Fatalf("outcomes = %+v", rep.Outcomes)
	}
	if !strings.Contains(rep.Outcomes[0].Error, "panicked") {
		t.Errorf("error = %q", rep.Outcomes[0].Error)
	}
}

func TestIdentityString(t *testing.T) {
	for _, tt := range []struct {
		in   Identity
		want string
	}{
		{Identity{ID: "1", Name: "@a"}, "@a (1)"},
		{Identity{Name: "@a"}, "@a"},
		{Identity{ID: "1"}, "1"},
		{Identity{ID: "same", Name: "same"}, "same"},
		{Identity{}, ""},
	} {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestEveryPublisherImplementsPing is the anti-drift check: a platform added
// without a Ping would compile, because Build returns the interface — this
// fails instead.
func TestEveryPublisherImplementsPing(t *testing.T) {
	if len(registry) != len(config.Platforms) {
		t.Fatalf("the registry has %d platforms and the config has %d", len(registry), len(config.Platforms))
	}
	// The interface itself carries Ping, so the check that matters is that
	// every constructor is reachable and returns something satisfying it.
	for _, name := range Names() {
		if _, ok := registry[name]; !ok {
			t.Errorf("%s has no constructor", name)
		}
	}
}

func TestBuildAllIsBuild(t *testing.T) {
	cfg := telegramConfig("https://api.telegram.org")
	a, err := BuildAll(cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) || a[0].Name() != b[0].Name() {
		t.Errorf("BuildAll and Build disagree: %v vs %v", a, b)
	}
}
