package ipinfo

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// sourceIPWhoIs 是基础地理信息源的名字，会出现在 available_sources / failed_sources 里。
const sourceIPWhoIs = "ipwho.is"

// geoRecord 是从 ipwho.is 归一化出来的地理/归属数据。
type geoRecord struct {
	Continent     string
	ContinentCode string
	Country       string
	CountryCode   string
	Region        string
	RegionCode    string
	City          string
	PostalCode    string
	Timezone      string
	Latitude      float64
	Longitude     float64
	ASNNumber     int
	ISP           string
	Org           string
	Domain        string
}

// ipWhoIsProvider 调用 https://ipwho.is/<ip>。
// 它没有鉴权要求，是 location 的主要来源，也是 ASN / 运营商的兜底来源。
type ipWhoIsProvider struct {
	baseURL string
	client  *http.Client
}

// ipWhoIsResponse 只声明用得到的字段；ipwho.is 在 success=false 时会带 message。
type ipWhoIsResponse struct {
	Success       bool    `json:"success"`
	Message       string  `json:"message"`
	Continent     string  `json:"continent"`
	ContinentCode string  `json:"continent_code"`
	Country       string  `json:"country"`
	CountryCode   string  `json:"country_code"`
	Region        string  `json:"region"`
	RegionCode    string  `json:"region_code"`
	City          string  `json:"city"`
	Postal        string  `json:"postal"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
	Timezone      struct {
		ID string `json:"id"`
	} `json:"timezone"`
	Connection struct {
		ASN    int    `json:"asn"`
		ISP    string `json:"isp"`
		Org    string `json:"org"`
		Domain string `json:"domain"`
	} `json:"connection"`
}

func newIPWhoIsProvider(baseURL string, client *http.Client) *ipWhoIsProvider {
	return &ipWhoIsProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (p *ipWhoIsProvider) Name() string { return sourceIPWhoIs }

func (p *ipWhoIsProvider) lookup(ctx context.Context, ip net.IP) (*geoRecord, error) {
	req, err := newJSONRequest(ctx, p.baseURL+"/"+ip.String())
	if err != nil {
		return nil, err
	}

	var payload ipWhoIsResponse
	if _, err := doJSON(ctx, p.client, req, &payload); err != nil {
		return nil, fmt.Errorf("ipwho.is: %w", err)
	}
	if !payload.Success {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "lookup failed"
		}
		return nil, fmt.Errorf("ipwho.is: %s", message)
	}

	return &geoRecord{
		Continent:     payload.Continent,
		ContinentCode: payload.ContinentCode,
		Country:       payload.Country,
		CountryCode:   payload.CountryCode,
		Region:        payload.Region,
		RegionCode:    payload.RegionCode,
		City:          payload.City,
		PostalCode:    payload.Postal,
		Timezone:      payload.Timezone.ID,
		Latitude:      payload.Latitude,
		Longitude:     payload.Longitude,
		ASNNumber:     payload.Connection.ASN,
		ISP:           payload.Connection.ISP,
		Org:           payload.Connection.Org,
		Domain:        payload.Connection.Domain,
	}, nil
}
