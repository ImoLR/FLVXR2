package repo

import (
	"context"
	"strings"
	"testing"

	"go-backend/internal/geoip"
	"go-backend/internal/store/model"
)

func TestNormalizeNodeRegion(t *testing.T) {
	for _, tc := range []struct {
		code, city, wantCode, wantCity string
		invalid                        bool
	}{
		{" hk ", " 葵涌 ", "HK", "葵涌", false},
		{"", "", "", "", false},
		{"JP", strings.Repeat("日", 50), "JP", strings.Repeat("日", 50), false},
		{"JP", strings.Repeat("日", 51), "", "", true},
		{"H", "", "", "", true},
		{"HKG", "", "", "", true},
		{"1A", "", "", "", true},
		{"中国", "", "", "", true},
	} {
		got, err := NormalizeNodeRegion(tc.code, tc.city)
		if (err != nil) != tc.invalid || got.Region != tc.wantCode || got.City != tc.wantCity {
			t.Fatalf("NormalizeNodeRegion(%q, %q) = %+v, %v", tc.code, tc.city, got, err)
		}
	}
}

func TestNodeRegionBackfillPreservesAdminAndRunsOnce(t *testing.T) {
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, node := range []model.Node{
		{ID: 1, Name: "manual", Region: "TW", RegionCity: "台北"},
		{ID: 2, Name: "empty", ServerIP: "8.8.8.8"},
		{ID: 3, Name: "edit during lookup", ServerIP: "1.1.1.1"},
		{ID: 4, Name: "unknown", ServerIP: "10.0.0.1"},
	} {
		if err := r.db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	detect := func(_, ip, _ string) geoip.DetectionResult {
		calls++
		if ip == "1.1.1.1" {
			if err := r.db.Model(&model.Node{}).Where("id = 3").Update("region", "JP").Error; err != nil {
				t.Fatal(err)
			}
		}
		if ip == "10.0.0.1" {
			return geoip.DetectionResult{Reason: "IPv4 为内网地址，请手动选择地区"}
		}
		return geoip.DetectionResult{Region: "US", IP: ip, Family: "v4", Source: "server_ip"}
	}
	if err := r.BackfillNodeRegions(context.Background(), detect); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("lookup calls = %d", calls)
	}
	if err := r.BackfillNodeRegions(context.Background(), detect); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("backfill ran twice: %d calls", calls)
	}
	for id, want := range map[int64]string{1: "TW", 2: "US", 3: "JP", 4: ""} {
		node, err := r.GetNodeByID(id)
		if err != nil || node.Region != want {
			t.Fatalf("node %d = %+v, %v; want %q", id, node, err, want)
		}
	}
}
