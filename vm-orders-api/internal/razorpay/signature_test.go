package razorpay

import (
	"errors"
	"strings"
	"testing"
)

const (
	testKeySecret     = "test_key_secret_do_not_use_in_production"
	testWebhookSecret = "test_webhook_secret_do_not_use_in_production"
)

// A realistic captured-payment body. Signature verification is over these
// exact bytes, so the test must not reformat it.
const sampleWebhookBody = `{"entity":"event","account_id":"acc_TEST","event":"payment.captured","contains":["payment"],"payload":{"payment":{"entity":{"id":"pay_TEST123","entity":"payment","amount":47438,"currency":"INR","status":"captured","order_id":"order_TEST123","method":"upi"}}},"created_at":1786600000}`

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(sampleWebhookBody)
	valid := SignWebhookBody(body, testWebhookSecret)

	tests := []struct {
		name      string
		body      []byte
		signature string
		secret    string
		wantErr   bool
	}{
		{
			name:      "valid signature",
			body:      body,
			signature: valid,
			secret:    testWebhookSecret,
		},
		{
			// The attack this exists to stop: anyone can POST a webhook, so a
			// body claiming a payment succeeded must not be believed without
			// the signature.
			name:      "tampered body — amount inflated",
			body:      []byte(strings.Replace(sampleWebhookBody, `"amount":47438`, `"amount":1`, 1)),
			signature: valid,
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			name:      "tampered body — different order",
			body:      []byte(strings.Replace(sampleWebhookBody, "order_TEST123", "order_ATTACK", 1)),
			signature: valid,
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			name:      "tampered body — one byte changed",
			body:      []byte(strings.Replace(sampleWebhookBody, "captured", "capturee", 1)),
			signature: valid,
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			// An attacker who knows the format but not our secret.
			name:      "signed with the wrong secret",
			body:      body,
			signature: SignWebhookBody(body, "the-wrong-secret"),
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			// Using the KEY secret where the WEBHOOK secret belongs. They are
			// different values for different purposes, and swapping them must
			// fail rather than quietly work.
			name:      "signed with the key secret instead of the webhook secret",
			body:      body,
			signature: SignWebhookBody(body, testKeySecret),
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			name:      "empty signature",
			body:      body,
			signature: "",
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			name:      "malformed hex",
			body:      body,
			signature: "not-hexadecimal-at-all",
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			name:      "truncated signature",
			body:      body,
			signature: valid[:len(valid)-2],
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			name:      "signature with trailing whitespace still verifies",
			body:      body,
			signature: "  " + valid + "\n",
			secret:    testWebhookSecret,
		},
		{
			name:      "empty body",
			body:      nil,
			signature: valid,
			secret:    testWebhookSecret,
			wantErr:   true,
		},
		{
			// A misconfigured deployment must fail closed, not accept
			// everything.
			name:      "empty secret",
			body:      body,
			signature: valid,
			secret:    "",
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyWebhookSignature(tc.body, tc.signature, tc.secret)

			if tc.wantErr {
				if err == nil {
					t.Fatal("SECURITY: signature accepted, want rejection")
				}
				if !errors.Is(err, ErrInvalidSignature) {
					t.Errorf("error = %v, want ErrInvalidSignature", err)
				}
				return
			}
			if err != nil {
				t.Errorf("valid signature rejected: %v", err)
			}
		})
	}
}

// TestWebhookSignatureIsByteExact — the signature is over raw bytes, so any
// re-serialisation of the JSON breaks it. This is why the gateway forwards
// this route's body verbatim.
func TestWebhookSignatureIsByteExact(t *testing.T) {
	body := []byte(sampleWebhookBody)
	signature := SignWebhookBody(body, testWebhookSecret)

	// Semantically identical JSON, different bytes: reordered keys, added
	// whitespace, and a pretty-printed form. All must fail.
	reserialised := []string{
		`{"event":"payment.captured","entity":"event","account_id":"acc_TEST","contains":["payment"],"payload":{"payment":{"entity":{"id":"pay_TEST123","entity":"payment","amount":47438,"currency":"INR","status":"captured","order_id":"order_TEST123","method":"upi"}}},"created_at":1786600000}`,
		sampleWebhookBody + "\n",
		" " + sampleWebhookBody,
		strings.ReplaceAll(sampleWebhookBody, `":`, `" :`),
	}

	for i, variant := range reserialised {
		t.Run("variant", func(t *testing.T) {
			if err := VerifyWebhookSignature([]byte(variant), signature, testWebhookSecret); err == nil {
				t.Errorf("variant %d: re-serialised body verified — the raw body was not preserved", i)
			}
		})
	}
}

func TestVerifyCallbackSignature(t *testing.T) {
	const (
		orderID   = "order_TEST123"
		paymentID = "pay_TEST123"
	)
	valid := SignCallback(orderID, paymentID, testKeySecret)

	tests := []struct {
		name      string
		orderID   string
		paymentID string
		signature string
		secret    string
		wantErr   bool
	}{
		{
			name:    "valid",
			orderID: orderID, paymentID: paymentID, signature: valid, secret: testKeySecret,
		},
		{
			// Swapping which order a payment belongs to would let someone pay
			// for a cheap order and claim an expensive one.
			name:    "different order id",
			orderID: "order_OTHER", paymentID: paymentID, signature: valid, secret: testKeySecret,
			wantErr: true,
		},
		{
			name:    "different payment id",
			orderID: orderID, paymentID: "pay_OTHER", signature: valid, secret: testKeySecret,
			wantErr: true,
		},
		{
			// The pipe is part of the signed payload. Without it,
			// ("ab","c") and ("a","bc") would collide.
			name:    "concatenation is delimited",
			orderID: orderID + "|" + paymentID, paymentID: "", signature: valid, secret: testKeySecret,
			wantErr: true,
		},
		{
			name:    "wrong secret",
			orderID: orderID, paymentID: paymentID,
			signature: SignCallback(orderID, paymentID, "wrong"), secret: testKeySecret,
			wantErr: true,
		},
		{
			name:    "webhook secret used instead of key secret",
			orderID: orderID, paymentID: paymentID,
			signature: SignCallback(orderID, paymentID, testWebhookSecret), secret: testKeySecret,
			wantErr: true,
		},
		{
			name:    "empty signature",
			orderID: orderID, paymentID: paymentID, signature: "", secret: testKeySecret,
			wantErr: true,
		},
		{
			name:    "empty secret",
			orderID: orderID, paymentID: paymentID, signature: valid, secret: "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyCallbackSignature(tc.orderID, tc.paymentID, tc.signature, tc.secret)

			if tc.wantErr {
				if err == nil {
					t.Fatal("SECURITY: callback signature accepted, want rejection")
				}
				return
			}
			if err != nil {
				t.Errorf("valid callback signature rejected: %v", err)
			}
		})
	}
}

// TestSignaturesAreDistinct — the two schemes sign different payloads with
// different secrets. A signature valid for one must never satisfy the other.
func TestSignaturesAreDistinct(t *testing.T) {
	const orderID, paymentID = "order_X", "pay_X"

	callbackSig := SignCallback(orderID, paymentID, testKeySecret)
	bodySig := SignWebhookBody([]byte(sampleWebhookBody), testWebhookSecret)

	if callbackSig == bodySig {
		t.Fatal("the two signature schemes produced the same value")
	}
	if err := VerifyWebhookSignature([]byte(sampleWebhookBody), callbackSig, testWebhookSecret); err == nil {
		t.Error("SECURITY: a callback signature satisfied webhook verification")
	}
	if err := VerifyCallbackSignature(orderID, paymentID, bodySig, testKeySecret); err == nil {
		t.Error("SECURITY: a webhook signature satisfied callback verification")
	}
}

// TestKnownVector pins the HMAC against a hand-computed value, so a future
// refactor cannot silently change what we sign.
func TestKnownVector(t *testing.T) {
	// Computed independently, and cross-checked against Python's hmac:
	//   printf '%s' 'order_1|pay_1' | openssl dgst -sha256 -hmac 'secret'
	const want = "52115a0d3400de9e86aade1f1b6eba9e8974604f4e267a9e9a16633a4c8dd2cb"

	got := SignCallback("order_1", "pay_1", "secret")
	if got != want {
		t.Errorf("SignCallback = %s, want %s\n"+
			"(if this changed deliberately, the signed payload format changed too)", got, want)
	}
}
