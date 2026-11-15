package core

import (
	"strconv"
	"strings"
	"testing"
)

const minimalRelayJSON = `{"Regions":{"901":{"RegionID":901,"RegionCode":"test","Nodes":[{"Name":"901a","RegionID":901,"HostName":"relay.example.com","DERPPort":443}]}}}`

func TestNormalizeRelayAccept(t *testing.T) {
	cases := []struct {
		name  string
		input string
		kind  RelayKind
	}{
		{"empty means the official map", "", RelayOfficial},
		{"blank means the official map", "   \n\t ", RelayOfficial},
		{"https url", "https://example.com/derpmap.json", RelayURL},
		{"http url", "http://127.0.0.1:8080/map.json", RelayURL},
		{"inline json", minimalRelayJSON, RelayInline},
		{"inline json with trailing commas", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":901,"HostName":"h.example","DERPPort":443,},],},},}`, RelayInline},
		{"derpMap wrapper", `"derpMap":` + minimalRelayJSON, RelayInline},
		{"commas inside strings are untouched", `{"Regions":{"901":{"RegionID":901,"RegionCode":"a,b","Nodes":[{"Name":"n,1","RegionID":901,"HostName":"h.example"}]}}}`, RelayInline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NormalizeRelay(tc.input)
			if err != nil {
				t.Fatalf("NormalizeRelay() error: %v", err)
			}
			if cfg.Kind != tc.kind {
				t.Fatalf("Kind = %v, want %v", cfg.Kind, tc.kind)
			}
			if tc.kind == RelayOfficial && cfg.Source != "" {
				t.Fatalf("official map should keep an empty source, got %q", cfg.Source)
			}
			if tc.kind == RelayURL && cfg.Source != strings.TrimSpace(tc.input) {
				t.Fatalf("Source = %q, want the trimmed URL", cfg.Source)
			}
			if tc.kind == RelayInline {
				if cfg.Map == nil || len(cfg.Map.Regions) == 0 {
					t.Fatal("inline map was not decoded")
				}
				if !strings.HasPrefix(cfg.Source, "{") {
					t.Fatalf("inline source should be canonical JSON, got %q", cfg.Source)
				}
				if strings.Contains(cfg.Source, ",}") || strings.Contains(cfg.Source, ",]") {
					t.Fatalf("trailing commas survived normalization: %s", cfg.Source)
				}
			}
		})
	}
}

func TestNormalizeRelayReject(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"ftp scheme", "ftp://example.com/map.json"},
		{"url with credentials", "https://user:pass@example.com/map.json"},
		{"url with fragment", "https://example.com/map.json#frag"},
		{"url without host", "https:///map.json"},
		{"garbage", "hello"},
		{"json array", `[{"RegionID":1}]`},
		{"no regions", `{"Regions":{}}`},
		{"region id mismatch", `{"Regions":{"901":{"RegionID":902,"Nodes":[{"Name":"a","RegionID":902,"HostName":"h.example"}]}}}`},
		{"node region mismatch", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":902,"HostName":"h.example"}]}}}`},
		{"no nodes", `{"Regions":{"901":{"RegionID":901,"Nodes":[]}}}`},
		{"node without name", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"RegionID":901,"HostName":"h.example"}]}}}`},
		{"node without hostname", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":901}]}}}`},
		{"hostname with a slash", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":901,"HostName":"http://x/y"}]}}}`},
		{"derp port out of range", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":901,"HostName":"h.example","DERPPort":70000}]}}}`},
		{"stun port out of range", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":901,"HostName":"h.example","STUNPort":70000}]}}}`},
		{"boolean port", `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","RegionID":901,"HostName":"h.example","DERPPort":true}]}}}`},
		{"too large", `{"Regions":{"901":{"RegionID":901,"RegionCode":"` + strings.Repeat("x", 140000) + `","Nodes":[{"Name":"a","RegionID":901,"HostName":"h.example"}]}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizeRelay(tc.input); err == nil {
				t.Fatalf("NormalizeRelay(%q) accepted an invalid map", tc.input[:min(len(tc.input), 80)])
			}
		})
	}
}

func TestNormalizeRelayRegionLimits(t *testing.T) {
	t.Run("128 regions accepted", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(`{"Regions":{`)
		for i := 1; i <= 128; i++ {
			if i > 1 {
				sb.WriteString(",")
			}
			sb.WriteString(`"` + strconv.Itoa(i) + `":{"RegionID":` + strconv.Itoa(i) + `,"Nodes":[{"Name":"n","RegionID":` + strconv.Itoa(i) + `,"HostName":"h.example"}]}`)
		}
		sb.WriteString(`}}`)
		if _, err := NormalizeRelay(sb.String()); err != nil {
			t.Fatalf("128 regions rejected: %v", err)
		}
	})
	t.Run("129 regions rejected", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(`{"Regions":{`)
		for i := 1; i <= 129; i++ {
			if i > 1 {
				sb.WriteString(",")
			}
			sb.WriteString(`"` + strconv.Itoa(i) + `":{"RegionID":` + strconv.Itoa(i) + `,"Nodes":[{"Name":"n","RegionID":` + strconv.Itoa(i) + `,"HostName":"h.example"}]}`)
		}
		sb.WriteString(`}}`)
		if _, err := NormalizeRelay(sb.String()); err == nil {
			t.Fatal("129 regions accepted")
		}
	})
	t.Run("33 nodes rejected", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(`{"Regions":{"1":{"RegionID":1,"Nodes":[`)
		for i := 0; i < 33; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`{"Name":"n` + strconv.Itoa(i) + `","RegionID":1,"HostName":"h.example"}`)
		}
		sb.WriteString(`]}}}`)
		if _, err := NormalizeRelay(sb.String()); err == nil {
			t.Fatal("33 nodes accepted")
		}
	})
}

func TestRelayOfficialHasNoMap(t *testing.T) {
	cfg, err := NormalizeRelay("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Map != nil || cfg.URL != "" {
		t.Fatalf("official config = %+v, want neither map nor url", cfg)
	}
	if !cfg.IsOfficial() {
		t.Fatal("IsOfficial() = false for the default map")
	}
}
