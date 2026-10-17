package unlock

import (
	"context"
	"net/http"
	"testing"
)

func TestAIServiceEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, host, path, body string
		code                   int
		want                   Status
		basis                  string
	}{
		{"openai blocked", "api.openai.com", "/compliance/cookie_requirements", `{"error":{"code":"unsupported_country_region_territory"}}`, 403, Blocked, BasisProbe},
		{"openai recognized", "api.openai.com", "/compliance/cookie_requirements", `{"country":"US","is_eu":false}`, 200, Partial, BasisProbe},
		{"openai challenge", "api.openai.com", "/compliance/cookie_requirements", `<html>Just a moment cf-chl-</html>`, 403, Unknown, BasisRegion},
		{"openai empty", "api.openai.com", "/compliance/cookie_requirements", `{}`, 200, Unknown, BasisRegion},
		{"openai current compliance", "api.openai.com", "/compliance/cookie_requirements", `{"cookie_consent_required":false}`, 200, Partial, BasisProbe},
		{"openai rate limit", "api.openai.com", "/compliance/cookie_requirements", `{"country":"US","is_eu":false}`, 429, Unknown, BasisRegion},
		{"claude challenge", "claude.ai", "/login", `<html>Claude Continue with Google Just a moment</html>`, 200, Unknown, BasisRegion},
		{"claude login", "claude.ai", "/login", `<html>Claude Continue with Google</html>`, 200, Partial, BasisProbe},
		{"claude blocked", "claude.ai", "/login", `Claude is not available in your region`, 403, Blocked, BasisProbe},
		{"gemini login", "gemini.google.com", "/", `boq_assistant-bard-web-server Gemini`, 200, Partial, BasisProbe},
		{"gemini current application", "gemini.google.com", "/", `<title>Google Gemini</title><script>WIZ_global_data={};</script>BardChatUi Sign in`, 200, Partial, BasisProbe},
		{"gemini challenge", "gemini.google.com", "/", `Gemini Chat with Gemini Before you continue`, 200, Unknown, BasisProbe},
		{"gemini blocked", "gemini.google.com", "/", `Gemini is not available in your country`, 403, Blocked, BasisProbe},
		{"gemini unknown", "gemini.google.com", "/", `<html>Sign in</html>`, 200, Unknown, BasisProbe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newFixture(map[string]func() (*http.Response, error){tc.host + tc.path: bodyResponse(tc.code, tc.body)})
			var got Result
			switch tc.host {
			case "api.openai.com":
				got = probeChatGPT(context.Background(), client, "US")
			case "claude.ai":
				got = probeClaude(context.Background(), client, "US")
			default:
				got = probeGemini(context.Background(), client, "US")
			}
			if got.Status != tc.want || got.Basis != tc.basis {
				t.Fatalf("got %+v, want %s/%s", got, tc.want, tc.basis)
			}
		})
	}
}

func TestMediaGenericAndChallengePagesStayUnknown(t *testing.T) {
	for _, body := range []string{
		`<link rel="canonical" href="https://www.netflix.com/title/81280792">`,
		`Just a moment cf-chl- {"id":81280792,"id":80018499}`,
	} {
		client, _ := newFixture(netflixRoutes(bodyResponse(200, body), bodyResponse(200, body)))
		if got := probeNetflix(context.Background(), client); got.Status != Unknown {
			t.Fatalf("generic page advertised as catalog evidence: %+v", got)
		}
	}
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"www.youtube.com/premium": bodyResponse(200, `Before you continue {"countryCode":"US","ad-free":1}`),
	})
	if got := probeYouTubePremium(context.Background(), client); got.Status != Unknown {
		t.Fatalf("consent page advertised as Premium evidence: %+v", got)
	}
}
