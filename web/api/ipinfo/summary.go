package ipinfo

import "context"

// NetworkSummary intentionally omits the queried IP and detailed location.
// It reuses the existing bounded provider cache instead of probing from a browser.
type NetworkSummary struct {
	Network      string `json:"network"`
	Organization string `json:"organization"`
	Country      string `json:"country"`
	Source       string `json:"source"`
	Stale        bool   `json:"stale"`
}

func (h *Handler) NodeNetworkSummary(ctx context.Context, address string) (*NetworkSummary, error) {
	ip, _, err := parsePublicIP(address)
	if err != nil {
		return nil, err
	}
	snapshot, meta, err := h.svc.Lookup(ctx, ip, false)
	if err != nil {
		return nil, err
	}
	kind := "网络类型未知"
	if snapshot.Network.CompanyType == "hosting" {
		kind = "数据中心"
	}
	if snapshot.Network.NetworkType == "mobile" {
		kind = "移动网络"
	}
	return &NetworkSummary{Network: kind, Organization: snapshot.Network.Organization, Country: snapshot.Location.CountryCode, Source: snapshot.Provider.BaseSource, Stale: meta.Stale}, nil
}
