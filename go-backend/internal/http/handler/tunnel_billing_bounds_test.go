package handler

import "testing"

func TestValidateTunnelBillingRequest(t *testing.T) {
	ok := []map[string]interface{}{
		{},
		{"flow": nil, "trafficRatio": nil},
		{"flow": float64(1), "trafficRatio": 0.01},
		{"flow": float64(2), "trafficRatio": float64(100)},
		{"flow": "2", "trafficRatio": "1.5"},
		{"flow": "", "trafficRatio": ""},
	}
	for _, req := range ok {
		if err := validateTunnelBillingRequest(req); err != nil {
			t.Fatalf("%v: unexpected error %v", req, err)
		}
	}
	bad := []map[string]interface{}{
		{"flow": float64(0)},
		{"flow": float64(-1)},
		{"flow": float64(3)},
		{"flow": float64(99999)},
		{"flow": 1.5},
		{"flow": "x"},
		{"trafficRatio": float64(0)},
		{"trafficRatio": -0.5},
		{"trafficRatio": 0.0099},
		{"trafficRatio": 100.0001},
		{"trafficRatio": float64(99999)},
		{"trafficRatio": "NaN"},
		{"trafficRatio": "+Inf"},
		{"trafficRatio": "x"},
	}
	for _, req := range bad {
		if err := validateTunnelBillingRequest(req); err == nil {
			t.Fatalf("%v: expected rejection", req)
		}
	}
}

func TestValidatePathBilling(t *testing.T) {
	for _, c := range []struct {
		flow  int
		ratio float64
	}{{0, 0}, {1, 0.01}, {2, 100}, {0, 1.5}, {2, 0}} {
		if err := validatePathBilling(c.flow, c.ratio); err != nil {
			t.Fatalf("flow=%d ratio=%v: unexpected error %v", c.flow, c.ratio, err)
		}
	}
	for _, c := range []struct {
		flow  int
		ratio float64
	}{{-1, 1}, {3, 1}, {99999, 1}, {1, -1}, {1, 0.001}, {1, 100.01}, {1, 99999}} {
		if err := validatePathBilling(c.flow, c.ratio); err == nil {
			t.Fatalf("flow=%d ratio=%v: expected rejection", c.flow, c.ratio)
		}
	}
}

func TestCheckTunnelImportBilling(t *testing.T) {
	row := func(flow, ratio interface{}) map[string]interface{} {
		return map[string]interface{}{"id": float64(7), "name": "t7", "flow": flow, "traffic_ratio": ratio}
	}
	okRows := []interface{}{row(float64(1), 1.0), row(float64(2), float64(100)), row(float64(0), float64(0)), row(nil, nil), row(float64(1), 0.01)}
	if err := checkTunnelImportBilling(map[string]interface{}{"tunnel": okRows, "path_tunnel": okRows}, []string{"tunnel", "path_tunnel"}); err != nil {
		t.Fatalf("valid rows rejected: %v", err)
	}
	for _, bad := range []map[string]interface{}{row(float64(99999), 1.0), row(float64(3), 1.0), row(float64(-1), 1.0), row(float64(1), float64(99999)), row(float64(1), -1.0), row(float64(1), 0.001), row("x", 1.0)} {
		for _, table := range []string{"tunnel", "path_tunnel"} {
			raw := map[string]interface{}{table: []interface{}{bad}}
			if err := checkTunnelImportBilling(raw, []string{table}); err == nil {
				t.Fatalf("%s row %v accepted", table, bad)
			}
			if err := checkTunnelImportBilling(raw, []string{"forward"}); err != nil {
				t.Fatalf("table not imported must be ignored: %v", err)
			}
		}
	}
}
