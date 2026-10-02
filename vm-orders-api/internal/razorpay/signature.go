// Package razorpay handles payment provider integration — CLAUDE.md §6.4.
//
// Everything here is security-critical. Two distinct signatures exist, over
// different data, with different secrets, and confusing them would let anyone
// mark an order paid:
//
//	browser callback: HMAC_SHA256(order_id + "|" + payment_id, KEY_SECRET)
//	webhook:          HMAC_SHA256(raw_request_body,            WEBHOOK_SECRET)
package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrInvalidSignature is returned by every verification failure.
//
// One error for "wrong signature", "malformed hex" and "empty input": telling
// a caller which would help them tune a forgery.
var ErrInvalidSignature = errors.New("razorpay: signature verification failed")

// VerifyCallbackSignature checks the signature returned to the BROWSER after
// Razorpay Checkout closes (CLAUDE.md §6.4 step 3).
//
// This is a UX hint only. It proves the browser saw a plausible success, and
// nothing more — the browser is attacker-controlled, so a forged call here
// must never be able to mark an order paid. The webhook is the source of
// truth (step 4).
func VerifyCallbackSignature(orderID, paymentID, signature, keySecret string) error {
	if orderID == "" || paymentID == "" || signature == "" || keySecret == "" {
		return ErrInvalidSignature
	}
	// The payload is exactly "order_id|payment_id" — the pipe is part of the
	// signed data, not a separator we chose.
	return verifyHexHMAC([]byte(orderID+"|"+paymentID), signature, keySecret)
}

// VerifyWebhookSignature checks X-Razorpay-Signature against the RAW request
// body (CLAUDE.md §6.4 step 4).
//
// rawBody must be the exact bytes received. Decoding and re-encoding JSON
// changes key order and whitespace, and the signature is over bytes — which
// is why the gateway forwards this route's body verbatim instead of parsing
// it.
func VerifyWebhookSignature(rawBody []byte, signature, webhookSecret string) error {
	if len(rawBody) == 0 || signature == "" || webhookSecret == "" {
		return ErrInvalidSignature
	}
	return verifyHexHMAC(rawBody, signature, webhookSecret)
}

// verifyHexHMAC computes HMAC-SHA256 over payload and compares it to a
// hex-encoded expected value in constant time.
func verifyHexHMAC(payload []byte, signature, secret string) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)

	// Decoding first means the comparison is over fixed-length bytes rather
	// than a caller-controlled string, and it rejects malformed hex outright.
	provided, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return ErrInvalidSignature
	}

	// hmac.Equal is constant-time. A byte-by-byte == would leak how much of a
	// forged signature was correct, letting an attacker recover it one byte
	// at a time from response timing.
	if !hmac.Equal(expected, provided) {
		return ErrInvalidSignature
	}
	return nil
}

// SignWebhookBody produces the signature Razorpay would send for a body.
//
// Exists for tests and for local webhook replay; it is never used to verify
// anything.
func SignWebhookBody(rawBody []byte, webhookSecret string) string {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignCallback produces the browser-callback signature, for tests.
func SignCallback(orderID, paymentID, keySecret string) string {
	mac := hmac.New(sha256.New, []byte(keySecret))
	mac.Write([]byte(orderID + "|" + paymentID))
	return hex.EncodeToString(mac.Sum(nil))
}
