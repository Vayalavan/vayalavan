package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
)

// mediaKind is what a content type resolves to: the gallery stores images and
// videos side by side, and the two differ in size limit, in whether they can
// serve as a cover, and in how a client renders them.
type mediaKind struct {
	kind      string // 'image' or 'video', matching product_media.kind
	extension string
}

// allowedMediaTypes are the content types a product image or video may be
// (CLAUDE.md §6.6).
//
// An allow-list, not a block-list: the value is bound into the presigned
// signature, so anything not listed here cannot be uploaded at all.
//
// The video formats are the three a phone actually produces — H.264 MP4 from
// both platforms, QuickTime from an iPhone, WebM from Android browsers — and
// all three play in a <video> tag on every browser the storefront supports.
var allowedMediaTypes = map[string]mediaKind{
	"image/jpeg":      {kind: mediaKindImage, extension: ".jpg"},
	"image/png":       {kind: mediaKindImage, extension: ".png"},
	"image/webp":      {kind: mediaKindImage, extension: ".webp"},
	"video/mp4":       {kind: mediaKindVideo, extension: ".mp4"},
	"video/quicktime": {kind: mediaKindVideo, extension: ".mov"},
	"video/webm":      {kind: mediaKindVideo, extension: ".webm"},
}

type presignRequest struct {
	ContentType string `json:"content_type"`
	// SizeBytes is what the client intends to upload. Checked before a URL is
	// issued so an oversized file is refused before any bytes move.
	SizeBytes int64 `json:"size_bytes"`
	// Filename is accepted and ignored. The extension comes from the declared
	// content type, which is the value actually bound into the signature; a
	// client-supplied name is not evidence of anything.
	Filename string `json:"filename"`
}

type presignResponse struct {
	// Kind tells the client which of image or video it just uploaded, so the
	// form can record it without re-deriving it from the content type.
	Kind string `json:"kind"`
	// UploadURL is a presigned PUT. The browser uploads straight to storage,
	// so multi-megabyte bodies never pass through this service.
	UploadURL string `json:"upload_url"`
	// Key is what the client sends back on the product payload. The key, not
	// the URL, is what gets stored (CLAUDE.md §5.2).
	Key string `json:"key"`
	// ContentType must be sent as the PUT's Content-Type header, byte for
	// byte, or the signature will not match.
	ContentType string `json:"content_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// PresignUpload issues a presigned PUT for one product image or video.
//
// The key is generated server-side from the AUTHENTICATED supplier id, never
// from client input: a client-supplied key would let one supplier overwrite
// another's images, or write outside the products/ prefix entirely.
func (a *API) PresignUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req presignRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	v := newValidation()

	contentType := strings.ToLower(strings.TrimSpace(req.ContentType))
	// Strip any "; charset=..." parameter before matching.
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	media, allowed := allowedMediaTypes[contentType]
	if !allowed {
		v.add("content_type", "must be a JPEG, PNG or WebP image, "+
			"or an MP4, MOV or WebM video")
	}

	// The cap depends on what is being uploaded, so it can only be applied
	// once the content type is known — an unrecognised type is held to the
	// image limit, the stricter of the two.
	maxBytes := a.limits.MaxImageBytes
	if media.kind == mediaKindVideo {
		maxBytes = a.limits.MaxVideoBytes
	}

	switch {
	case req.SizeBytes <= 0:
		v.add("size_bytes", "is required")
	case req.SizeBytes > maxBytes:
		v.add("size_bytes", fmt.Sprintf("must be at most %d bytes (%d MB)",
			maxBytes, maxBytes/(1<<20)))
	}

	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	key := fmt.Sprintf("products/%s/%s%s", supplierID, uuid.NewString(), media.extension)

	uploadURL, err := a.storage.PresignPut(ctx, key, contentType)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, presignResponse{
		Kind:        media.kind,
		UploadURL:   uploadURL,
		Key:         key,
		ContentType: contentType,
		ExpiresIn:   int(a.storage.PresignPutTTL().Seconds()),
	})
}
