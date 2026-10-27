package locations

import (
	"encoding/json"
	"testing"
)

func TestCatalogCounts(t *testing.T) {
	got := Summaries()
	want := []struct {
		name  string
		count int
	}{
		{"宜宾", 74},
		{"李白河", 47},
		{"汇东", 20},
	}
	if len(got) != len(want) {
		t.Fatalf("campuses = %+v", got)
	}
	total := 0
	for i, item := range got {
		if item.Name != want[i].name || item.Count != want[i].count {
			t.Fatalf("summary[%d] = %+v, want %s/%d", i, item, want[i].name, want[i].count)
		}
		total += item.Count
	}
	if total != 141 {
		t.Fatalf("total = %d, want 141", total)
	}
}

func TestCatalogDataIsConsistent(t *testing.T) {
	for _, campus := range All() {
		if campus.Name == "" || len(campus.Locations) == 0 {
			t.Fatalf("empty campus: %+v", campus)
		}
		for i, loc := range campus.Locations {
			if loc.Address == "" || loc.LocationJSON == "" {
				t.Fatalf("%s[%d] empty: %+v", campus.Name, i, loc)
			}
			var parsed struct {
				Point   []float64 `json:"point"`
				Address string    `json:"address"`
			}
			if err := json.Unmarshal([]byte(loc.LocationJSON), &parsed); err != nil {
				t.Fatalf("%s[%d] invalid JSON: %v", campus.Name, i, err)
			}
			if len(parsed.Point) != 2 {
				t.Fatalf("%s[%d] point = %+v", campus.Name, i, parsed.Point)
			}
			if parsed.Address != loc.Address {
				t.Fatalf("%s[%d] address mismatch: %q vs %q", campus.Name, i, parsed.Address, loc.Address)
			}
			if parsed.Point[0] != loc.Longitude || parsed.Point[1] != loc.Latitude {
				t.Fatalf("%s[%d] point order mismatch: %+v vs lng=%v lat=%v", campus.Name, i, parsed.Point, loc.Longitude, loc.Latitude)
			}
			if loc.Latitude == 0 && loc.Longitude == 0 {
				t.Fatalf("%s[%d] zero point", campus.Name, i)
			}
		}
	}
}

func TestFindAndPick(t *testing.T) {
	if !HasCampus(" 宜宾 ") {
		t.Fatal("trimmed campus not found")
	}
	if HasCampus("北京") {
		t.Fatal("unknown campus accepted")
	}
	campus, ok := Find("李白河")
	if !ok || campus.Name != "李白河" || len(campus.Locations) != 47 {
		t.Fatalf("find = %+v %v", campus, ok)
	}

	yb, _ := Find(DefaultCampus)
	allowed := make(map[string]bool, len(yb.Locations))
	for _, loc := range yb.Locations {
		allowed[loc.Address] = true
	}
	for i := 0; i < 100; i++ {
		loc, ok := Pick("不存在")
		if !ok || !allowed[loc.Address] {
			t.Fatalf("fallback pick = %+v %v", loc, ok)
		}
	}
	loc, ok := Pick("汇东")
	if !ok || len(loc.Address) == 0 {
		t.Fatalf("pick 汇东 = %+v %v", loc, ok)
	}
}

func TestAllReturnsCopies(t *testing.T) {
	all := All()
	all[0].Locations[0].Address = "mutated"
	fresh, _ := Find("宜宾")
	if fresh.Locations[0].Address == "mutated" {
		t.Fatal("All returned internal storage")
	}
}
