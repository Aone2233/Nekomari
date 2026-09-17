package ipinfo

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIpInfoASNListUnmarshal 单测 network-info 的 asns 兼容解析。
//
// 线上实测返回的是字符串数组（["15169"]），早期实现用 []int 解码会让整个
// network-info 调用失败（route 与 ASN 兜底一起丢掉），这里把两种形态都钉住。
func TestIpInfoASNListUnmarshal(t *testing.T) {
	cases := []struct {
		raw  string
		want []int
	}{
		{`["15169"]`, []int{15169}},
		{`[15169]`, []int{15169}},
		{`["AS15169"]`, []int{15169}},
		{`["15169", 24940]`, []int{15169, 24940}},
		{`[]`, []int{}},
		{`["bogus"]`, []int{}},
		{`[0]`, []int{}},
		{`null`, []int{}},
	}

	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			var list asnList
			if err := json.Unmarshal([]byte(testCase.raw), &list); err != nil {
				t.Fatalf("unmarshal %s: %v", testCase.raw, err)
			}
			if len(list) != len(testCase.want) {
				t.Fatalf("asnList = %v, want %v", list, testCase.want)
			}
			for index := range testCase.want {
				if list[index] != testCase.want[index] {
					t.Errorf("asnList[%d] = %d, want %d", index, list[index], testCase.want[index])
				}
			}
		})
	}
}

// TestIpInfoNetworkInfoAcceptsStringASNs 端到端确认字符串形态不会让整段降级。
func TestIpInfoNetworkInfoAcceptsStringASNs(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	// 上游只提供 network-info（geo 挂掉），ASN 必须由字符串形态的 asns 兜底。
	upstreams.setGeo(failWith(http.StatusInternalServerError))

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	_, data, _ := decodeSuccess(t, recorder)

	if got := asString(t, asObject(t, data, "network"), "asn"); got != "AS15169" {
		t.Errorf("network.asn = %q, want AS15169 from RIPEstat fallback", got)
	}
	if got := asString(t, asObject(t, data, "network"), "route"); got != "1.2.3.0/24" {
		t.Errorf("network.route = %q, want 1.2.3.0/24", got)
	}
}

// TestIpInfoASNRegistrationParsing 单测 whois records 的解析。
func TestIpInfoASNRegistrationParsing(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCountry string
		wantRIR     string
		wantErr     bool
	}{
		{
			name: "arin-capitalised-country",
			body: `{"data":{"records":[[{"key":"ASNumber","value":"15169"},{"key":"source","value":"ARIN"}],
				[{"key":"OrgName","value":"Google LLC"},{"key":"Country","value":"US"}]]}}`,
			wantCountry: "US",
			wantRIR:     "ARIN",
		},
		{
			name: "apnic-lowercase-country",
			body: `{"data":{"records":[[{"key":"descr","value":"Korea Telecom"},{"key":"country","value":"kr"},
				{"key":"source","value":"APNIC"},{"key":"source","value":"KRNIC"}]]}}`,
			wantCountry: "KR",
			wantRIR:     "APNIC",
		},
		{
			name: "ripe-has-no-country",
			body: `{"data":{"records":[[{"key":"aut-num","value":"AS24940"},{"key":"as-name","value":"HETZNER-AS"},
				{"key":"source","value":"RIPE"}]]}}`,
			wantCountry: "",
			wantRIR:     "RIPE",
		},
		{
			name:    "rir-prefix-match",
			body:    `{"data":{"records":[[{"key":"source","value":"RIPE NCC"}]]}}`,
			wantRIR: "RIPE",
		},
		{
			name:    "non-rir-source-ignored",
			body:    `{"data":{"records":[[{"key":"source","value":"KRNIC"},{"key":"descr","value":"KT"}]]}}`,
			wantRIR: "",
		},
		{
			name: "full-country-name-rejected",
			body: `{"data":{"records":[[{"key":"Country","value":"United States"},{"key":"source","value":"ARIN"}]]}}`,
			// 只接受两位国家码，避免把不可比的字符串塞进分类判定。
			wantCountry: "",
			wantRIR:     "ARIN",
		},
		{
			name:    "empty-records",
			body:    `{"data":{"records":[]}}`,
			wantErr: true,
		},
		{
			name:    "null-data",
			body:    `{"data":null}`,
			wantErr: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := newWhoisTestProvider(t, testCase.body)
			got, err := provider.asnRegistration(context.Background(), 15169)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Country != testCase.wantCountry {
				t.Errorf("country = %q, want %q", got.Country, testCase.wantCountry)
			}
			if got.RIR != testCase.wantRIR {
				t.Errorf("rir = %q, want %q", got.RIR, testCase.wantRIR)
			}
		})
	}
}

// TestIpInfoNetworkInfoParsesRealShape 单测 network-info 的真实响应形态。
func TestIpInfoNetworkInfoParsesRealShape(t *testing.T) {
	provider := newWhoisTestProvider(t, `{"status":"ok","data_call_name":"network-info","data":{"asns":["15169"],"prefix":"8.8.8.0/24"}}`)

	prefix, asns, err := provider.networkInfo(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("networkInfo failed: %v", err)
	}
	if prefix != "8.8.8.0/24" {
		t.Errorf("prefix = %q, want 8.8.8.0/24", prefix)
	}
	if len(asns) != 1 || asns[0] != 15169 {
		t.Errorf("asns = %v, want [15169]", asns)
	}

	if _, err := provider.reverseDNS(context.Background(), net.ParseIP("8.8.8.8")); err != nil {
		t.Fatalf("reverseDNS failed: %v", err)
	}
}

// newWhoisTestProvider 返回一个把任意路径都回固定 JSON 的 RIPEstat provider。
func newWhoisTestProvider(t *testing.T, body string) *ripeStatProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return newRIPEstatProvider(server.URL, server.Client())
}
