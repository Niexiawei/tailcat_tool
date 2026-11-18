package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"tailscale.com/tailcfg"
)

// maxRelayConfigSize bounds a DERP map, in bytes, before and after cleaning.
const maxRelayConfigSize = 131072

// hosting limits mirror what the Swift build enforced.
const (
	maxRelayRegions        = 128
	maxRelayNodesPerRegion = 32
)

var relayHostPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// RelayKind says where a device's DERP map comes from.
type RelayKind int

// The relay sources a device can use.
const (
	// RelayOfficial uses tailcat's default public map.
	RelayOfficial RelayKind = iota
	// RelayURL points at a JSON map served over HTTP(S).
	RelayURL
	// RelayInline carries the map itself.
	RelayInline
)

// RelayConfig is a validated DERP source. Source is what gets persisted in the
// configuration file: a URL, canonical JSON, or empty for the official map.
type RelayConfig struct {
	Kind   RelayKind
	Source string
	URL    string
	Map    *tailcfg.DERPMap
}

// IsOfficial reports whether the device uses tailcat's default map.
func (r RelayConfig) IsOfficial() bool { return r.Kind == RelayOfficial }

// NormalizeRelay validates a user-supplied DERP source: an HTTP(S) URL, or
// inline JSON (optionally wrapped in "derpMap", optionally with trailing
// commas). Empty input means the official map.
func NormalizeRelay(input string) (RelayConfig, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return RelayConfig{Kind: RelayOfficial}, nil
	}
	if len(value) > maxRelayConfigSize {
		return RelayConfig{}, Error("DERP 配置不能超过 128 KiB")
	}
	if u, err := url.Parse(value); err == nil &&
		(u.Scheme == "http" || u.Scheme == "https") &&
		u.Host != "" && u.User == nil && u.Fragment == "" {
		return RelayConfig{Kind: RelayURL, Source: value, URL: value}, nil
	}

	cleaned := stripTrailingCommas(value)
	if strings.HasPrefix(cleaned, `"derpMap"`) {
		cleaned = "{" + cleaned + "}"
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &envelope); err != nil {
		return RelayConfig{}, Error("请填写 HTTP/HTTPS JSON 链接，或有效的 DERP JSON 内容")
	}
	payload := []byte(cleaned)
	if wrapped, ok := envelope["derpMap"]; ok {
		payload = wrapped
	}
	var dm tailcfg.DERPMap
	if err := json.Unmarshal(payload, &dm); err != nil {
		return RelayConfig{}, Error("请填写 HTTP/HTTPS JSON 链接，或有效的 DERP JSON 内容")
	}
	if err := validateRelayMap(&dm); err != nil {
		return RelayConfig{}, err
	}
	pretty, err := json.MarshalIndent(&dm, "", "  ")
	if err != nil {
		return RelayConfig{}, Error("请填写 HTTP/HTTPS JSON 链接，或有效的 DERP JSON 内容")
	}
	if len(pretty) > maxRelayConfigSize {
		return RelayConfig{}, Error("DERP 配置不能超过 128 KiB")
	}
	return RelayConfig{Kind: RelayInline, Source: string(pretty), Map: &dm}, nil
}

func validateRelayMap(dm *tailcfg.DERPMap) error {
	if len(dm.Regions) == 0 || len(dm.Regions) > maxRelayRegions {
		return Error("DERP JSON 需要包含 1–128 个 Regions 区域")
	}
	for id, region := range dm.Regions {
		if region == nil || id <= 0 || region.RegionID != id {
			return Error("DERP 区域 ID 需要与键一致，每个区域需要 1–32 个节点")
		}
		if len(region.Nodes) == 0 || len(region.Nodes) > maxRelayNodesPerRegion {
			return Error("DERP 区域 ID 需要与键一致，每个区域需要 1–32 个节点")
		}
		for _, node := range region.Nodes {
			if node == nil || node.Name == "" || node.HostName == "" || node.RegionID != id ||
				!relayHostPattern.MatchString(node.HostName) {
				return Error("DERP 节点需要有效的名称、HostName 和匹配的 RegionID")
			}
			if node.DERPPort < 0 || node.DERPPort > 65535 {
				return Error("DERP 节点的 DERPPort 端口无效")
			}
			if node.STUNPort < -1 || node.STUNPort > 65535 {
				return Error("DERP 节点的 STUNPort 端口无效")
			}
		}
	}
	return nil
}

// stripTrailingCommas removes commas that sit right before a closing brace or
// bracket, leaving string contents and escapes untouched.
func stripTrailingCommas(value string) string {
	chars := []rune(value)
	var out strings.Builder
	out.Grow(len(value))
	quoted, escaped := false, false
	for i, char := range chars {
		if quoted {
			out.WriteRune(char)
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				quoted = false
			}
			continue
		}
		if char == '"' {
			quoted = true
		}
		if char == ',' {
			next := i + 1
			for next < len(chars) && isSpace(chars[next]) {
				next++
			}
			if next < len(chars) && (chars[next] == '}' || chars[next] == ']') {
				continue
			}
		}
		out.WriteRune(char)
	}
	return out.String()
}

func isSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// relayHTTPClient downloads one map. The timeout keeps a stalled server from
// leaving the detail page spinning forever.
var relayHTTPClient = &http.Client{Timeout: 10 * time.Second}

// fetchRelayMap downloads one URL and validates it exactly the way the editor
// validates inline JSON: same size bound, same region and node rules.
func fetchRelayMap(ctx context.Context, rawURL string) (RelayConfig, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return RelayConfig{}, Error("下载 DERP 地图失败，请检查网络或链接")
	}
	response, err := relayHTTPClient.Do(request)
	if err != nil {
		return RelayConfig{}, relayDownloadError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RelayConfig{}, Errorf("下载 DERP 地图失败：服务器返回 %d", response.StatusCode)
	}
	// Read one byte past the limit so an oversized body is detected without
	// buffering all of it.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRelayConfigSize+1))
	if err != nil {
		return RelayConfig{}, relayDownloadError(err)
	}
	if len(body) > maxRelayConfigSize {
		return RelayConfig{}, Error("下载的 DERP 地图超过 128 KiB")
	}
	cfg, err := NormalizeRelay(string(body))
	if err != nil || cfg.IsOfficial() {
		return RelayConfig{}, Error("下载的内容不是有效的 DERP 地图")
	}
	return cfg, nil
}

// relayDownloadError reports a transport failure without echoing the URL back
// into the UI.
func relayDownloadError(err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return Error("下载 DERP 地图超时，请检查网络后重试")
	}
	return Error("下载 DERP 地图失败，请检查网络或链接")
}
