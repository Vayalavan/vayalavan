package api

import (
	"testing"
)

// The gallery's kinds and size caps are the boundary a supplier's upload
// crosses, so they are asserted directly rather than through a handler.

// Every content type the presign endpoint accepts must map to the kind and
// extension the rest of the service assumes — a video mapped to "image" would
// become a product's cover and render as a broken <img> on every card.
func TestAllowedMediaTypes(t *testing.T) {
	tests := []struct {
		contentType   string
		wantKind      string
		wantExtension string
	}{
		{"image/jpeg", mediaKindImage, ".jpg"},
		{"image/png", mediaKindImage, ".png"},
		{"image/webp", mediaKindImage, ".webp"},
		{"video/mp4", mediaKindVideo, ".mp4"},
		{"video/quicktime", mediaKindVideo, ".mov"},
		{"video/webm", mediaKindVideo, ".webm"},
	}

	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			got, ok := allowedMediaTypes[tc.contentType]
			if !ok {
				t.Fatalf("%s is not allowed, want allowed", tc.contentType)
			}
			if got.kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.kind, tc.wantKind)
			}
			if got.extension != tc.wantExtension {
				t.Errorf("extension = %q, want %q", got.extension, tc.wantExtension)
			}
		})
	}

	// An allow-list, not a block-list: anything absent cannot be uploaded at
	// all, because the content type is bound into the presigned signature.
	for _, contentType := range []string{
		"image/svg+xml", // scriptable, and would be served from our own origin
		"image/gif",
		"video/x-msvideo",
		"application/pdf",
		"text/html",
		"",
	} {
		if _, ok := allowedMediaTypes[contentType]; ok {
			t.Errorf("%q is allowed, want refused", contentType)
		}
	}
}

func TestValidateMedia(t *testing.T) {
	const maxMedia = 8

	t.Run("assigns sort order from array position", func(t *testing.T) {
		v := newValidation()
		got := validateMedia([]mediaPayload{
			{Kind: "image", ObjectKey: "products/s/a.jpg", ContentType: "image/jpeg"},
			{Kind: "video", ObjectKey: "products/s/b.mp4", ContentType: "video/mp4"},
			{Kind: "image", ObjectKey: "products/s/c.png", ContentType: "image/png"},
		}, maxMedia, v)

		if err := v.err(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("parsed %d items, want 3", len(got))
		}
		for i, item := range got {
			if item.SortOrder != int32(i) {
				t.Errorf("item %d has sort_order %d, want %d", i, item.SortOrder, i)
			}
		}
		// Order is preserved exactly: it is what decides the cover.
		if got[0].ObjectKey != "products/s/a.jpg" || got[2].ObjectKey != "products/s/c.png" {
			t.Errorf("order changed: %+v", got)
		}
	})

	t.Run("empty gallery is valid", func(t *testing.T) {
		v := newValidation()
		got := validateMedia(nil, maxMedia, v)
		if err := v.err(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("parsed %d items, want 0", len(got))
		}
	})

	t.Run("rejects an unknown kind", func(t *testing.T) {
		v := newValidation()
		validateMedia([]mediaPayload{
			{Kind: "audio", ObjectKey: "products/s/a.mp3"},
		}, maxMedia, v)
		if v.err() == nil {
			t.Error("kind 'audio' was accepted, want a validation error")
		}
	})

	t.Run("rejects a missing object key", func(t *testing.T) {
		v := newValidation()
		validateMedia([]mediaPayload{
			{Kind: "image", ObjectKey: "   "},
		}, maxMedia, v)
		if v.err() == nil {
			t.Error("blank object_key was accepted, want a validation error")
		}
	})

	// Would violate UNIQUE (product_id, object_key) at insert time; catching
	// it here names the offending row instead of returning a 409.
	t.Run("rejects the same object twice", func(t *testing.T) {
		v := newValidation()
		validateMedia([]mediaPayload{
			{Kind: "image", ObjectKey: "products/s/a.jpg"},
			{Kind: "image", ObjectKey: "products/s/a.jpg"},
		}, maxMedia, v)
		if v.err() == nil {
			t.Error("duplicate object_key was accepted, want a validation error")
		}
	})

	t.Run("rejects more than the cap", func(t *testing.T) {
		items := make([]mediaPayload, maxMedia+1)
		for i := range items {
			items[i] = mediaPayload{
				Kind:      "image",
				ObjectKey: "products/s/" + string(rune('a'+i)) + ".jpg",
			}
		}
		v := newValidation()
		if got := validateMedia(items, maxMedia, v); len(got) != 0 {
			t.Errorf("parsed %d items past the cap, want 0", len(got))
		}
		if v.err() == nil {
			t.Errorf("%d items were accepted with a cap of %d", len(items), maxMedia)
		}
	})
}
