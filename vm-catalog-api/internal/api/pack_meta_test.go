package api

import "testing"

// A pack carries an optional line of detail, exactly as a size code does.
//
// The pair matters more than either alone: a supplier meets the same
// "Detail (optional)" field at both levels of the form, and if the two
// behaved differently — one trimming, the other not; one capping at 80, the
// other rejecting — the form would be lying about being one idea.

func product(packs ...unitPayload) productPayload {
	return productPayload{
		Name: "Pomegranate",
		Type: "fruit",
		SizeCodes: []sizeCodePayload{{
			Code:  "L2",
			Meta:  "260 g +",
			Packs: packs,
		}},
	}
}

func pack(label, meta string) unitPayload {
	return unitPayload{Label: label, Meta: meta, WeightGrams: 2000, PriceRupees: "2100"}
}

func TestPackMeta(t *testing.T) {
	limits := productLimits{MaxSizeCodes: 10, MaxMedia: 8}

	t.Run("is carried through, trimmed", func(t *testing.T) {
		parsed, err := validateProduct(product(pack("2 Kg Box", "  6-8 fruit  ")), limits)
		if err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
		got := parsed.SizeCodes[0].Packs[0].Meta
		if got == nil || *got != "6-8 fruit" {
			t.Fatalf("pack meta = %v, want %q", got, "6-8 fruit")
		}
	})

	t.Run("empty is nil, not an empty string", func(t *testing.T) {
		// Null, so a client can ask "is there a detail?" without also having
		// to know that "" and "   " mean no.
		parsed, err := validateProduct(product(pack("2 Kg Box", "   ")), limits)
		if err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
		if got := parsed.SizeCodes[0].Packs[0].Meta; got != nil {
			t.Fatalf("pack meta = %q, want nil", *got)
		}
	})

	t.Run("caps at 80, the same as a size code's", func(t *testing.T) {
		long := ""
		for range 100 {
			long += "x"
		}
		parsed, err := validateProduct(product(pack("2 Kg Box", long)), limits)
		if err != nil {
			t.Fatalf("validateProduct: %v", err)
		}
		packMeta := parsed.SizeCodes[0].Packs[0].Meta
		if packMeta == nil || len(*packMeta) != 80 {
			t.Fatalf("pack meta length = %v, want 80", packMeta)
		}
		// The database CHECK is the same 80 either way; truncating rather
		// than rejecting is what the size code already did, and a save that
		// fails on a decorative field would be worse than a shortened one.
		if len(*packMeta) > 80 {
			t.Fatalf("pack meta exceeds the column's CHECK constraint")
		}
	})
}
