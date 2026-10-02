package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// SelfTest proves the presigned upload path end to end, exactly as a browser
// would walk it:
//
//  1. presign a PUT          (server-side, signed for Endpoint)
//  2. PUT the bytes over plain HTTP    <- no AWS SDK, like the browser
//  3. presign a GET
//  4. GET the bytes over plain HTTP    <- no AWS SDK, like the browser
//  5. compare, then delete
//
// Steps 2 and 4 deliberately use net/http rather than the SDK. The SDK would
// re-sign the request and mask the exact failure this test exists to catch: a
// signature that is valid only for an endpoint the browser cannot reach. If
// this passes, presigned URLs work in a real browser.
func (c *Client) SelfTest(ctx context.Context, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("storage selftest: generating nonce: %w", err)
	}
	key := "_selftest/" + hex.EncodeToString(nonce[:]) + ".txt"
	payload := []byte("vayal storage selftest " + time.Now().UTC().Format(time.RFC3339Nano))
	const contentType = "text/plain"

	logger.InfoContext(ctx, "storage selftest starting",
		slog.String("endpoint", c.cfg.Endpoint),
		slog.String("bucket", c.cfg.Bucket),
		slog.String("key", key),
	)

	// --- 1. presign the upload ---------------------------------------------
	putURL, err := c.PresignPut(ctx, key, contentType)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "  1/5 presigned PUT ok")

	// --- 2. upload as the browser would ------------------------------------
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("storage selftest: building PUT: %w", err)
	}
	// Must match the content type bound into the signature, or MinIO rejects
	// the request with SignatureDoesNotMatch.
	putReq.Header.Set("Content-Type", contentType)

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		return fmt.Errorf("storage selftest: PUT to presigned URL failed "+
			"(is %s reachable from here?): %w", c.cfg.Endpoint, err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(putResp.Body, 2048))
		return fmt.Errorf("storage selftest: PUT returned %d: %s", putResp.StatusCode, body)
	}
	logger.InfoContext(ctx, "  2/5 uploaded via presigned URL ok",
		slog.Int("bytes", len(payload)))

	// --- 3. presign the download -------------------------------------------
	getURL, err := c.PresignGet(ctx, key)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "  3/5 presigned GET ok")

	// --- 4. download as the browser would ----------------------------------
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("storage selftest: building GET: %w", err)
	}
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return fmt.Errorf("storage selftest: GET from presigned URL failed: %w", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(getResp.Body, 2048))
		return fmt.Errorf("storage selftest: GET returned %d: %s", getResp.StatusCode, body)
	}

	got, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("storage selftest: reading downloaded body: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("storage selftest: downloaded %d bytes, expected %d — content differs",
			len(got), len(payload))
	}
	logger.InfoContext(ctx, "  4/5 downloaded via presigned URL, bytes match")

	// --- 5. clean up --------------------------------------------------------
	if err := c.Delete(ctx, key); err != nil {
		return err
	}
	logger.InfoContext(ctx, "  5/5 cleaned up test object")

	logger.InfoContext(ctx, "storage selftest PASSED — presigned URLs work from the browser",
		slog.String("endpoint", c.cfg.Endpoint))
	return nil
}
