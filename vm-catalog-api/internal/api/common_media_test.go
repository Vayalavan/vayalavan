package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// Media that belongs to the PRODUCT — the field, the packing shed, the grower
// — is shown after the pictures of whichever grade the customer is looking at.
//
// The concatenation is done once, server-side, so a phone and a browser cannot
// disagree about the order. These assert the rule where it lives.

func medium(key string, kind string, sizeCode *uuid.UUID) store.ProductMedium {
	return store.ProductMedium{
		ID: uuid.New(), SizeCodeID: sizeCode, ObjectKey: key, Kind: kind,
	}
}

func TestGalleryPutsCommonMediaLast(t *testing.T) {
	m2, l2 := uuid.New(), uuid.New()
	tree := productTree{
		Media: map[uuid.UUID][]store.ProductMedium{
			m2: {medium("m2-a.jpg", mediaKindImage, &m2), medium("m2-b.jpg", mediaKindImage, &m2)},
			l2: {medium("l2-a.jpg", mediaKindImage, &l2)},
		},
		CommonMedia: []store.ProductMedium{
			medium("field.jpg", mediaKindImage, nil),
			medium("packing.mp4", mediaKindVideo, nil),
		},
	}

	for _, tc := range []struct {
		name      string
		sizeCode  uuid.UUID
		wantOrder []string
	}{
		{"a grade with its own pictures", m2,
			[]string{"m2-a.jpg", "m2-b.jpg", "field.jpg", "packing.mp4"}},
		{"another grade gets the same common tail", l2,
			[]string{"l2-a.jpg", "field.jpg", "packing.mp4"}},
		{"a grade with none shows the common ones alone", uuid.New(),
			[]string{"field.jpg", "packing.mp4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tree.gallery(tc.sizeCode)
			if len(got) != len(tc.wantOrder) {
				t.Fatalf("gallery has %d items, want %d", len(got), len(tc.wantOrder))
			}
			for i, want := range tc.wantOrder {
				if got[i].ObjectKey != want {
					t.Errorf("item %d = %s, want %s", i, got[i].ObjectKey, want)
				}
			}
		})
	}

	// The common rows are appended to a COPY. Building one grade's gallery
	// must not leave them stuck on the map entry, or the next grade — or the
	// next request against the same tree — would show them twice.
	if got := len(tree.Media[m2]); got != 2 {
		t.Errorf("M2 now holds %d items, want the 2 it started with", got)
	}
	if got := len(tree.gallery(m2)); got != 4 {
		t.Errorf("second call returned %d items, want 4 — the first call mutated the tree", got)
	}
}

// A product with no common media is the ordinary case, and must not pay for
// the feature with a copy of every gallery.
func TestGalleryWithoutCommonMediaIsTheGradesOwn(t *testing.T) {
	m2 := uuid.New()
	own := []store.ProductMedium{medium("m2-a.jpg", mediaKindImage, &m2)}
	tree := productTree{Media: map[uuid.UUID][]store.ProductMedium{m2: own}}

	got := tree.gallery(m2)
	if len(got) != 1 || got[0].ObjectKey != "m2-a.jpg" {
		t.Fatalf("gallery = %+v, want just the grade's own", got)
	}
	if &got[0] != &own[0] {
		t.Error("the grade's own slice was copied when there was nothing to append")
	}
}

// The cover is the first IMAGE of the combined list, so a grower who
// photographed the field but not the crates still gets a thumbnail — and a
// video never becomes one.
func TestCommonMediaCanProvideTheCover(t *testing.T) {
	m2 := uuid.New()
	tree := productTree{
		Media: map[uuid.UUID][]store.ProductMedium{
			// Video only: nothing here can be painted as a still.
			m2: {medium("m2-clip.mp4", mediaKindVideo, &m2)},
		},
		CommonMedia: []store.ProductMedium{
			medium("packing.mp4", mediaKindVideo, nil),
			medium("field.jpg", mediaKindImage, nil),
		},
	}

	gallery := tree.gallery(m2)
	var cover string
	for _, m := range gallery {
		if m.Kind == mediaKindImage {
			cover = m.ObjectKey
			break
		}
	}
	if cover != "field.jpg" {
		t.Errorf("cover = %q, want field.jpg — the first image of the combined list", cover)
	}
}
