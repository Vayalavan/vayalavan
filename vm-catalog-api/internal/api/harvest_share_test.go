package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
)

// The share of a harvest each grade is.
//
// Display-only data, but it still has to be arithmetic somebody could defend:
// a split that adds up to 130% would make every rarity badge on the product
// wrong at once, and a zero would claim a grade nobody grows. Absent has to
// survive as absent — a grower who has not estimated their split is the normal
// starting state, and inventing a 0 for them puts a claim on the storefront
// that nobody made.

func share(pct int) *int {
	return &pct
}

// gradedProduct builds a product with one size code per share given. A nil
// entry is a grade whose share the grower did not state.
func gradedProduct(shares ...*int) productPayload {
	codes := make([]sizeCodePayload, 0, len(shares))
	for i, s := range shares {
		codes = append(codes, sizeCodePayload{
			Code:            string(rune('A' + i)),
			HarvestSharePct: s,
			Packs: []unitPayload{
				{Label: "1 Kg", WeightGrams: 1000, PriceRupees: "100"},
			},
		})
	}
	return productPayload{Name: "Pomegranate", Type: "fruit", SizeCodes: codes}
}

func TestHarvestShare(t *testing.T) {
	limits := productLimits{MaxSizeCodes: 10, MaxMedia: 8}

	t.Run("a realistic split is carried through", func(t *testing.T) {
		// 55 / 30 / 12 / 3 — the shape of an actual season, and the case the
		// storefront draws gold through silver from.
		parsed, err := validateProduct(
			gradedProduct(share(55), share(30), share(12), share(3)), limits)
		if err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
		want := []int16{55, 30, 12, 3}
		for i, w := range want {
			got := parsed.SizeCodes[i].HarvestSharePct
			if got == nil || *got != w {
				t.Fatalf("size code %d share = %v, want %d", i, got, w)
			}
		}
	})

	t.Run("absent stays absent", func(t *testing.T) {
		parsed, err := validateProduct(gradedProduct(nil), limits)
		if err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
		if got := parsed.SizeCodes[0].HarvestSharePct; got != nil {
			t.Fatalf("share = %d, want nil", *got)
		}
	})

	t.Run("a partial split is fine", func(t *testing.T) {
		// A grower may estimate only the grades worth calling out. 15% stated
		// and the rest unlabelled is not an error — it is the common case.
		if _, err := validateProduct(gradedProduct(share(15), nil, nil), limits); err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
	})

	t.Run("exactly 100 is allowed", func(t *testing.T) {
		if _, err := validateProduct(gradedProduct(share(60), share(40)), limits); err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
	})

	t.Run("over 100 in total is rejected", func(t *testing.T) {
		_, err := validateProduct(gradedProduct(share(60), share(50), share(20)), limits)
		if err == nil {
			t.Fatal("validateProduct accepted a 130% harvest")
		}
		// Names the total, so the grower can see which numbers to fix rather
		// than being told only that something is wrong. The message rides in
		// the details map against size_codes, which is where the form reads
		// field errors from.
		var httpErr *httpx.Error
		if !errors.As(err, &httpErr) {
			t.Fatalf("error is not an httpx.Error: %v", err)
		}
		problem, _ := httpErr.Details["size_codes"].(string)
		if !strings.Contains(problem, "130") {
			t.Fatalf("size_codes problem does not name the total: %q", problem)
		}
	})

	t.Run("out of range shares are rejected", func(t *testing.T) {
		for _, pct := range []int{0, -1, 101, 1000} {
			if _, err := validateProduct(gradedProduct(share(pct)), limits); err == nil {
				t.Fatalf("validateProduct accepted a share of %d", pct)
			}
		}
	})

	t.Run("1 and 100 are in range", func(t *testing.T) {
		for _, pct := range []int{1, 100} {
			if _, err := validateProduct(gradedProduct(share(pct)), limits); err != nil {
				t.Fatalf("validateProduct rejected a share of %d: %v", pct, err)
			}
		}
	})
}
