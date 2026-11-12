package entitlement

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

// fakeJWS builds an unsigned compact JWS — decodeJWSPayload only reads the
// payload segment, signature verification is a documented production TODO.
func fakeJWS(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"ES256"}`)) + "." + enc([]byte(payload)) + "." + enc([]byte("sig"))
}

func TestDecodeTransactionOwnership(t *testing.T) {
	expires := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	base := `"originalTransactionId":"1000000000000001","productId":"com.pickhour.plus.yearly","expiresDate":%d,"environment":"Production"`

	cases := []struct {
		name     string
		payload  string
		wantOwn  string
		wantPlus bool
	}{
		{
			name:    "purchased",
			payload: fmt.Sprintf(`{%s,"ownershipType":"PURCHASED"}`, fmt.Sprintf(base, expires)),
			wantOwn: "purchased",
		},
		{
			name:    "family shared",
			payload: fmt.Sprintf(`{%s,"ownershipType":"FAMILY_SHARED"}`, fmt.Sprintf(base, expires)),
			wantOwn: "family_shared",
		},
		{
			name:    "missing ownershipType defaults purchased",
			payload: fmt.Sprintf(`{%s}`, fmt.Sprintf(base, expires)),
			wantOwn: "purchased",
		},
		{
			name:    "unknown value defaults purchased",
			payload: fmt.Sprintf(`{%s,"ownershipType":"SOMETHING_ELSE"}`, fmt.Sprintf(base, expires)),
			wantOwn: "purchased",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := decodeTransaction(fakeJWS(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if sub.Ownership != tc.wantOwn {
				t.Fatalf("ownership = %q, want %q", sub.Ownership, tc.wantOwn)
			}
			if sub.Status != "active" {
				t.Fatalf("status = %q, want active", sub.Status)
			}
		})
	}
}
