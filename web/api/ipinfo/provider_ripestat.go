package ipinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// sourceRIPEstat 是 RIPEstat（stat.ripe.net）的名字。
const sourceRIPEstat = "stat.ripe.net"

// asnRegistration 是 ASN 的注册信息，country 用于原生/广播判定。
type asnRegistration struct {
	Holder  string
	Country string // 两位国家码，取不到时为空
	RIR     string // 只可能是 ARIN/RIPE/APNIC/LACNIC/AFRINIC
}

// ripeStatProvider 调用 RIPEstat 的数据接口（无鉴权）：
//   - network-info：该 IP 的宣告前缀与 ASN 列表；
//   - whois：ASN 的注册国家与 RIR；
//   - reverse-dns-ip：反向解析，作为 domain 的兜底。
type ripeStatProvider struct {
	baseURL string
	client  *http.Client
}

func newRIPEstatProvider(baseURL string, client *http.Client) *ripeStatProvider {
	return &ripeStatProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (p *ripeStatProvider) Name() string { return sourceRIPEstat }

// asnList 兼容 network-info 返回的两种形态：数字数组与字符串数组。
//
// 实测（2026-09）线上返回的是字符串数组：{"asns":["15169"]}。
// 用 []int 直接解码会整段失败，所以这里自己解析。
type asnList []int

func (l *asnList) UnmarshalJSON(data []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	parsed := make([]int, 0, len(items))
	for _, item := range items {
		var number int
		if err := json.Unmarshal(item, &number); err == nil {
			if number > 0 {
				parsed = append(parsed, number)
			}
			continue
		}
		var text string
		if err := json.Unmarshal(item, &text); err != nil {
			continue
		}
		text = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(text)), "AS")
		if number, err := strconv.Atoi(text); err == nil && number > 0 {
			parsed = append(parsed, number)
		}
	}
	*l = parsed
	return nil
}

// networkInfo 返回该 IP 的宣告前缀与 ASN 列表。
func (p *ripeStatProvider) networkInfo(ctx context.Context, ip net.IP) (string, []int, error) {
	url := fmt.Sprintf("%s/data/network-info/data.json?resource=%s", p.baseURL, ip.String())
	req, err := newJSONRequest(ctx, url)
	if err != nil {
		return "", nil, err
	}

	var payload struct {
		Data struct {
			Prefix string  `json:"prefix"`
			ASNs   asnList `json:"asns"`
		} `json:"data"`
	}
	if _, err := doJSON(ctx, p.client, req, &payload); err != nil {
		return "", nil, fmt.Errorf("ripestat network-info: %w", err)
	}
	return payload.Data.Prefix, payload.Data.ASNs, nil
}

// asnRegistration 返回 ASN 的注册国家与 RIR。
//
// 为什么用 whois 而不是 as-overview：实测（2026-09）线上 as-overview（version
// 1.3）只返回 holder / block / announced，没有任何国家字段；注册国家只在 whois
// 的 records 里，而且键名随 RIR 不同（ARIN 写 "Country"，APNIC 写 "country"）。
// RIPE 区域的 ASN 连 whois 都不带国家（只有 source=RIPE），此时上层会把分类
// 退化成 unknown —— 契约允许这个取值，好过瞎猜。
func (p *ripeStatProvider) asnRegistration(ctx context.Context, asn int) (*asnRegistration, error) {
	if asn <= 0 {
		return nil, fmt.Errorf("ripestat whois: invalid asn %d", asn)
	}
	url := fmt.Sprintf("%s/data/whois/data.json?resource=AS%d", p.baseURL, asn)
	req, err := newJSONRequest(ctx, url)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Data struct {
			Records [][]struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"records"`
		} `json:"data"`
	}
	if _, err := doJSON(ctx, p.client, req, &payload); err != nil {
		return nil, fmt.Errorf("ripestat whois: %w", err)
	}

	registration := &asnRegistration{}
	for _, record := range payload.Data.Records {
		for _, entry := range record {
			switch strings.ToLower(strings.TrimSpace(entry.Key)) {
			case "country":
				if registration.Country == "" {
					registration.Country = normalizeCountryCode(entry.Value)
				}
			case "source":
				// 同一个 ASN 可能同时登记在 NIR（如 KRNIC）与 RIR 下，只认 RIR。
				if registration.RIR == "" {
					registration.RIR = normalizeRIR(entry.Value)
				}
			case "orgname", "org-name", "as-name", "descr", "owner":
				if registration.Holder == "" {
					registration.Holder = strings.TrimSpace(entry.Value)
				}
			}
		}
	}
	if registration.Country == "" && registration.RIR == "" && registration.Holder == "" {
		return nil, fmt.Errorf("ripestat whois: no usable records for AS%d", asn)
	}
	return registration, nil
}

// reverseDNS 返回该 IP 的主 PTR 名称，失败不代表错误（很多 IP 没有 PTR）。
func (p *ripeStatProvider) reverseDNS(ctx context.Context, ip net.IP) (string, error) {
	url := fmt.Sprintf("%s/data/reverse-dns-ip/data.json?resource=%s", p.baseURL, ip.String())
	req, err := newJSONRequest(ctx, url)
	if err != nil {
		return "", err
	}

	var payload struct {
		Data struct {
			Name   string   `json:"name"`
			Result []string `json:"result"`
		} `json:"data"`
	}
	if _, err := doJSON(ctx, p.client, req, &payload); err != nil {
		return "", fmt.Errorf("ripestat reverse-dns-ip: %w", err)
	}
	if name := strings.TrimSpace(payload.Data.Name); name != "" {
		return name, nil
	}
	for _, name := range payload.Data.Result {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", nil
}

// normalizeCountryCode 只接受两位字母的 ISO 国家码；其它形态（全称、空值）
// 一律返回空串，避免把不可比的字符串塞进分类判定。
func normalizeCountryCode(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != 2 || !isAlpha(code[0]) || !isAlpha(code[1]) {
		return ""
	}
	return code
}

func isAlpha(char byte) bool {
	return (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
}

// normalizeRIR 把 source 字段归一成五个 RIR 之一，非 RIR（NIR、历史 source）返回空串。
func normalizeRIR(raw string) string {
	value := strings.ToUpper(strings.TrimSpace(raw))
	switch {
	case value == "ARIN":
		return "ARIN"
	case strings.HasPrefix(value, "RIPE"):
		return "RIPE"
	case value == "APNIC":
		return "APNIC"
	case value == "LACNIC":
		return "LACNIC"
	case value == "AFRINIC":
		return "AFRINIC"
	default:
		return ""
	}
}

// domainFromPTR 把 PTR 名称裁成注册域，只取最后两段：
// "host.example.com" -> "example.com"。
//
// 这只是 ipwho.is 没给域名时的兜底显示值，不引入公共后缀列表；
// 对 "dns.google" 这类只有两段的 PTR 会原样返回，取不到两段就返回空串。
func domainFromPTR(name string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(name), ".")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
