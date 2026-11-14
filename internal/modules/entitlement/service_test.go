package entitlement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// memRepo is an in-memory Repository mirroring the SQL semantics that matter:
// the last_signed_date guard, terminal revocation and the device cap.
type memRepo struct {
	devices map[string]bool
	subs    map[string]Subscription
	links   map[string]map[string]bool // otid -> devices
	notes   map[string]string          // uuid -> status
}

func newMemRepo() *memRepo {
	return &memRepo{devices: map[string]bool{}, subs: map[string]Subscription{}, links: map[string]map[string]bool{}, notes: map[string]string{}}
}

func (r *memRepo) DeviceExists(_ context.Context, id string) (bool, error) { return r.devices[id], nil }

func (r *memRepo) UpsertSubscription(_ context.Context, s Subscription) (bool, error) {
	if old, ok := r.subs[s.OriginalTransactionID]; ok {
		revokedOld := old.Status == "revoked" || old.Status == "refunded"
		revokedNew := s.Status == "revoked" || s.Status == "refunded"
		if !old.LastSignedDate.Before(s.LastSignedDate) || (revokedOld && !revokedNew) {
			return false, nil
		}
	}
	r.subs[s.OriginalTransactionID] = s
	return true, nil
}

func (r *memRepo) LinkDevice(_ context.Context, dev, otid string, max int) error {
	if r.links[otid] == nil {
		r.links[otid] = map[string]bool{}
	}
	if r.links[otid][dev] {
		return nil
	}
	if len(r.links[otid]) >= max {
		return ErrDeviceLimit
	}
	r.links[otid][dev] = true
	return nil
}

func (r *memRepo) ActiveSubscription(_ context.Context, dev string) (*Subscription, error) {
	for otid, devs := range r.links {
		if devs[dev] {
			if s := r.subs[otid]; s.Status == "active" {
				return &s, nil
			}
		}
	}
	return nil, nil
}

func (r *memRepo) RecordNotification(_ context.Context, n NotificationRecord) error {
	if _, ok := r.notes[n.UUID]; !ok {
		r.notes[n.UUID] = "received"
	}
	return nil
}

func (r *memRepo) SubscriptionLastSignedDate(_ context.Context, otid string) (time.Time, bool, error) {
	s, ok := r.subs[otid]
	return s.LastSignedDate, ok, nil
}
func (r *memRepo) MarkNotificationProcessed(_ context.Context, u string) error {
	r.notes[u] = "processed"
	return nil
}
func (r *memRepo) MarkNotificationIgnored(_ context.Context, u string) error {
	r.notes[u] = "ignored"
	return nil
}
func (r *memRepo) MarkNotificationFailed(_ context.Context, u, _ string) error {
	r.notes[u] = "failed"
	return nil
}

func (p *testPKI) tx(t *testing.T, otid string, signed time.Time, extra string) string {
	t.Helper()
	return p.sign(t, fmt.Sprintf(`{"originalTransactionId":%q,"productId":"com.pickhour.plus.yearly","signedDate":%d,"bundleId":%q,"environment":"Production","expiresDate":%d%s}`,
		otid, signed.UnixMilli(), testBundle, time.Now().Add(24*time.Hour).UnixMilli(), extra))
}

func (p *testPKI) notification(t *testing.T, id string, signed time.Time, innerTx string) string {
	t.Helper()
	data := fmt.Sprintf(`"environment":"Production","bundleId":%q`, testBundle)
	if innerTx != "" {
		data += fmt.Sprintf(`,"signedTransactionInfo":%q`, innerTx)
	}
	return p.sign(t, fmt.Sprintf(`{"notificationType":"REFUND","notificationUUID":%q,"signedDate":%d,"data":{%s}}`, id, signed.UnixMilli(), data))
}

func TestLinkRejectsForgedTransaction(t *testing.T) {
	svc, _, repo := newTestService(t, defaultCfg())
	dev := uuid.NewString()
	repo.devices[dev] = true
	forged := "eyJhbGciOiJFUzI1NiJ9." + b64url(`{"originalTransactionId":"1","productId":"p","signedDate":1,"bundleId":"`+testBundle+`","environment":"Production"}`) + ".c2ln"
	if _, err := svc.LinkTransaction(t.Context(), dev, forged); !errors.Is(err, ErrInvalidJWS) {
		t.Fatalf("forged JWS: %v", err)
	}
	if len(repo.subs) != 0 {
		t.Fatal("forged transaction reached the repository")
	}
}

func TestLinkAccountTokenBinding(t *testing.T) {
	svc, pki, repo := newTestService(t, defaultCfg())
	dev, other := uuid.NewString(), uuid.NewString()
	repo.devices[dev] = true
	now := time.Now()

	// Token naming another device: refused.
	_, err := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "1", now, fmt.Sprintf(`,"appAccountToken":%q`, other)))
	if !errors.Is(err, ErrAccountTokenMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	// Token equal to the device (any case): accepted.
	if _, err := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "1", now, fmt.Sprintf(`,"appAccountToken":%q`, strings.ToUpper(dev)))); err != nil {
		t.Fatalf("matching token: %v", err)
	}
	// No token / non-UUID token: accepted (the app does not set it yet).
	if _, err := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "2", now, ``)); err != nil {
		t.Fatalf("no token: %v", err)
	}
	if _, err := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "3", now, `,"appAccountToken":"not-a-uuid"`)); err != nil {
		t.Fatalf("non-uuid token: %v", err)
	}
}

func TestLinkDeviceCap(t *testing.T) {
	svc, pki, repo := newTestService(t, defaultCfg())
	now := time.Now()
	var last string
	for i := 0; i < MaxDevicesPerSubscription; i++ {
		last = uuid.NewString()
		repo.devices[last] = true
		if _, err := svc.LinkTransaction(t.Context(), last, pki.tx(t, "1", now.Add(time.Duration(i)*time.Second), ``)); err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}
	extra := uuid.NewString()
	repo.devices[extra] = true
	if _, err := svc.LinkTransaction(t.Context(), extra, pki.tx(t, "1", now.Add(time.Minute), ``)); !errors.Is(err, ErrDeviceLimit) {
		t.Fatalf("over cap: %v", err)
	}
	// Re-linking an already-linked device still works at the cap.
	if _, err := svc.LinkTransaction(t.Context(), last, pki.tx(t, "1", now.Add(2*time.Minute), ``)); err != nil {
		t.Fatalf("relink at cap: %v", err)
	}
}

func TestRefundedCannotBeReactivatedByOlderTransaction(t *testing.T) {
	svc, pki, repo := newTestService(t, defaultCfg())
	dev := uuid.NewString()
	repo.devices[dev] = true
	t0 := time.Now().Add(-48 * time.Hour)

	// 1. Purchase (signed t0), linked.
	if e, err := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "1", t0, ``)); err != nil || e.Plan != PlanPlus {
		t.Fatalf("purchase: %v %v", e, err)
	}
	// 2. Refund notification signed t0+1h, with a revoked inner transaction.
	t1 := t0.Add(time.Hour)
	revoked := pki.tx(t, "1", t1, fmt.Sprintf(`,"revocationDate":%d`, t1.UnixMilli()))
	if err := svc.ProcessNotification(t.Context(), pki.notification(t, uuid.NewString(), t1, revoked)); err != nil {
		t.Fatal(err)
	}
	if e, _ := svc.Entitlement(t.Context(), dev); e.Plan != PlanFree {
		t.Fatal("refund did not revoke")
	}
	// 3. Replay the old, validly signed pre-refund transaction (signedDate t0).
	e, err := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "1", t0, ``))
	if err != nil {
		t.Fatal(err)
	}
	if e.Plan != PlanFree || repo.subs["1"].Status != "revoked" {
		t.Fatalf("old transaction reactivated a refunded subscription: plan=%s status=%s", e.Plan, repo.subs["1"].Status)
	}
	// 4. Even a newer-signed, non-revoked transaction cannot un-revoke.
	t2 := t1.Add(time.Hour)
	if e, _ := svc.LinkTransaction(t.Context(), dev, pki.tx(t, "1", t2, ``)); e.Plan != PlanFree || repo.subs["1"].Status != "revoked" {
		t.Fatalf("newer non-revoked transaction un-revoked: status=%s", repo.subs["1"].Status)
	}
}

func TestNotificationOrderingAndNestedVerification(t *testing.T) {
	svc, pki, repo := newTestService(t, defaultCfg())
	t0 := time.Now().Add(-time.Hour)

	// Late notification (signed before what we already hold) is ignored.
	if err := svc.ProcessNotification(t.Context(), pki.notification(t, uuid.NewString(), t0.Add(10*time.Minute), pki.tx(t, "1", t0.Add(10*time.Minute), ``))); err != nil {
		t.Fatal(err)
	}
	stale := uuid.NewString()
	if err := svc.ProcessNotification(t.Context(), pki.notification(t, stale, t0, pki.tx(t, "1", t0, ``))); err != nil {
		t.Fatal(err)
	}
	if repo.notes[stale] != "ignored" {
		t.Fatalf("stale notification status = %q", repo.notes[stale])
	}
	// Newer notification applies and the stored date is Apple's signedDate.
	fresh := uuid.NewString()
	t1 := t0.Add(30 * time.Minute)
	if err := svc.ProcessNotification(t.Context(), pki.notification(t, fresh, t1, pki.tx(t, "1", t1, ``))); err != nil {
		t.Fatal(err)
	}
	if repo.notes[fresh] != "processed" || repo.subs["1"].LastSignedDate.UnixMilli() != t1.UnixMilli() {
		t.Fatalf("fresh: %q last=%v", repo.notes[fresh], repo.subs["1"].LastSignedDate)
	}

	// A nested transaction with a bad signature rejects the whole notification
	// (it must not be silently dropped and marked processed).
	other := newTestPKI(t, pkiOpts{})
	bad := uuid.NewString()
	err := svc.ProcessNotification(t.Context(), pki.notification(t, bad, t1.Add(time.Minute), other.tx(t, "2", t1, ``)))
	if !errors.Is(err, ErrInvalidNotice) {
		t.Fatalf("nested forged tx: %v", err)
	}
	if _, ok := repo.notes[bad]; ok || repo.subs["2"].OriginalTransactionID != "" {
		t.Fatal("rejected notification was recorded/applied")
	}
	// Wrong bundle id on the outer payload.
	wrong := pki.sign(t, fmt.Sprintf(`{"notificationType":"X","notificationUUID":%q,"signedDate":1,"data":{"environment":"Production","bundleId":"com.evil"}}`, uuid.NewString()))
	if err := svc.ProcessNotification(t.Context(), wrong); !errors.Is(err, ErrInvalidNotice) {
		t.Fatalf("wrong bundle: %v", err)
	}
}

func b64url(s string) string {
	return strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(b64([]byte(s))), "=")
}
