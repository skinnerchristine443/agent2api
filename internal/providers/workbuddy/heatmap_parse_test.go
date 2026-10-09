package workbuddy

import (
	"encoding/json"
	"testing"
)

// 活动日历以多种形态到达；每种都必须能解析，而形态错误的必须是一个显式错误
// 而不是空日历。
func TestParseGrowthHeatmapShapes(t *testing.T) {
	t.Run("heatmap envelope", func(t *testing.T) {
		heatmap, err := parseGrowthHeatmap(json.RawMessage(`{"heatmap":{"cells":[{"date":"2026-10-06","score":3},{"date":"2026-10-07","score":"0"}]}}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(heatmap.Cells) != 2 || heatmap.Cells[0].Score != 3 || heatmap.Cells[1].Score != 0 {
			t.Fatalf("heatmap=%+v", heatmap)
		}
	})
	t.Run("cells at data level", func(t *testing.T) {
		heatmap, err := parseGrowthHeatmap(json.RawMessage(`{"cells":[{"date":"2026-10-06","score":1}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(heatmap.Cells) != 1 || heatmap.Cells[0].Date != "2026-10-06" {
			t.Fatalf("heatmap=%+v", heatmap)
		}
	})
	t.Run("bare array", func(t *testing.T) {
		heatmap, err := parseGrowthHeatmap(json.RawMessage(`[{"date":"2026-10-06","score":2}]`))
		if err != nil {
			t.Fatal(err)
		}
		if len(heatmap.Cells) != 1 || heatmap.Cells[0].Score != 2 {
			t.Fatalf("heatmap=%+v", heatmap)
		}
	})
	t.Run("malformed cells are skipped", func(t *testing.T) {
		heatmap, err := parseGrowthHeatmap(json.RawMessage(`{"cells":[{"score":9}, {"date":""}, {"date":"2026-10-06","score":1}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(heatmap.Cells) != 1 {
			t.Fatalf("heatmap=%+v", heatmap)
		}
	})
	t.Run("wrong shape errors", func(t *testing.T) {
		if _, err := parseGrowthHeatmap(json.RawMessage(`{"foo":1}`)); err == nil {
			t.Fatal("a response without cells must be an explicit error")
		}
	})
}
