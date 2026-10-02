package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMockCatalogsAreValid guards mocks/: every catalogue parses, names a
// supplier, and every product has a grade with a priced pack; every media
// file it names exists and is a type the seed (and an upload) accepts. A
// broken edit fails here, not halfway through someone's ./scripts/dev-setup.sh.
func TestMockCatalogsAreValid(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "mocks", "suppliers", "*", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no mocks/suppliers/*/catalog.json found")
	}
	for _, file := range files {
		t.Run(filepath.Base(filepath.Dir(file)), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var cat mockCatalog
			if err := json.Unmarshal(raw, &cat); err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if cat.Supplier.ID.String() == "00000000-0000-0000-0000-000000000000" {
				t.Error("supplier.id is missing")
			}
			base := filepath.Dir(file)
			checkMedia := func(where string, items []mockMedia) {
				for _, m := range items {
					ext := strings.ToLower(filepath.Ext(m.File))
					if _, ok := mockContentTypes[ext]; !ok {
						t.Errorf("%s: %s has an unsupported type %q", where, m.File, ext)
					}
					if _, err := os.Stat(filepath.Join(base, m.File)); err != nil {
						t.Errorf("%s: %s: %v", where, m.File, err)
					}
				}
			}
			for _, p := range cat.Products {
				checkMedia(p.Name, p.Media)
				if len(p.SizeCodes) == 0 {
					t.Errorf("%s: no size codes", p.Name)
				}
				for _, sc := range p.SizeCodes {
					checkMedia(p.Name+" "+sc.Code, sc.Media)
					if len(sc.Packs) == 0 {
						t.Errorf("%s %s: no packs", p.Name, sc.Code)
					}
					for _, pk := range sc.Packs {
						if pk.PricePaise <= 0 || pk.WeightGrams <= 0 {
							t.Errorf("%s %s %s: price %d paise, %d g", p.Name, sc.Code, pk.Label, pk.PricePaise, pk.WeightGrams)
						}
					}
				}
			}
		})
	}
}
