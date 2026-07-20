package cleanup

import (
	"context"
	"errors"
	"io"
	"testing"

	"share/backend/internal/store"
)

type fakeCleanupStore struct {
	items          []store.ReceiptDeletion
	acked, retried []int64
	ackErr         error
	claimLimit     int
	claimErr       error
}

func (f *fakeCleanupStore) DeleteExpiredSessions(context.Context) (int64, error) { return 0, nil }
func (f *fakeCleanupStore) DeleteExpiredOTPCodes(context.Context) (int64, error) { return 0, nil }
func (f *fakeCleanupStore) DeleteExpiredWebauthnCeremonies(context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeCleanupStore) DeleteExpiredBillSessions(context.Context) (int64, error) { return 0, nil }
func (f *fakeCleanupStore) ClaimReceiptDeletions(_ context.Context, limit int) ([]store.ReceiptDeletion, error) {
	f.claimLimit = limit
	return f.items, f.claimErr
}

func TestProcessQueueClaimFailureMakesNoStorageCalls(t *testing.T) {
	s := &fakeCleanupStore{items: []store.ReceiptDeletion{{ID: 6, Path: "a/x.jpg"}}, claimErr: errors.New("claim failed")}
	r := &fakeReceiptStorage{}
	processQueue(context.Background(), s, r)
	if r.calls != 0 || len(s.acked) != 0 || len(s.retried) != 0 {
		t.Fatalf("calls=%d ack=%v retry=%v", r.calls, s.acked, s.retried)
	}
}
func (f *fakeCleanupStore) AckReceiptDeletion(_ context.Context, id int64) error {
	f.acked = append(f.acked, id)
	err := f.ackErr
	f.ackErr = nil
	return err
}
func (f *fakeCleanupStore) RetryReceiptDeletion(_ context.Context, id int64) error {
	f.retried = append(f.retried, id)
	return nil
}

type fakeReceiptStorage struct {
	err         error
	calls       int
	sawDeadline bool
}

func (f *fakeReceiptStorage) Save(context.Context, string, io.Reader) (string, error) { return "", nil }
func (f *fakeReceiptStorage) Open(context.Context, string) (io.ReadCloser, error)     { return nil, nil }
func (f *fakeReceiptStorage) Delete(ctx context.Context, _ string) error {
	f.calls++
	_, f.sawDeadline = ctx.Deadline()
	return f.err
}
func (f *fakeReceiptStorage) Compress(context.Context, string) (string, int, int, error) {
	return "", 0, 0, nil
}

func TestProcessQueueSuccessAckAndBatch(t *testing.T) {
	s := &fakeCleanupStore{items: []store.ReceiptDeletion{{ID: 1, Path: "a/x.jpg"}, {ID: 2, Path: "b/y.jpg"}}}
	r := &fakeReceiptStorage{}
	processQueue(context.Background(), s, r)
	if r.calls != 2 || len(s.acked) != 2 || len(s.retried) != 0 || !r.sawDeadline || s.claimLimit != 100 {
		t.Fatalf("calls=%d ack=%v retry=%v deadline=%v limit=%d", r.calls, s.acked, s.retried, r.sawDeadline, s.claimLimit)
	}
}

func TestProcessQueueDeleteFailureRetries(t *testing.T) {
	s := &fakeCleanupStore{items: []store.ReceiptDeletion{{ID: 3, Path: "a/x.jpg"}}}
	r := &fakeReceiptStorage{err: errors.New("delete failed")}
	processQueue(context.Background(), s, r)
	if len(s.retried) != 1 || s.retried[0] != 3 || len(s.acked) != 0 {
		t.Fatalf("ack=%v retry=%v", s.acked, s.retried)
	}
}

func TestProcessQueueDeleteTimeoutRetries(t *testing.T) {
	s := &fakeCleanupStore{items: []store.ReceiptDeletion{{ID: 4, Path: "a/x.jpg"}}}
	r := &fakeReceiptStorage{err: context.DeadlineExceeded}
	processQueue(context.Background(), s, r)
	if len(s.retried) != 1 {
		t.Fatalf("retry=%v", s.retried)
	}
}

func TestProcessQueueAckFailureLeavesSafeRetry(t *testing.T) {
	s := &fakeCleanupStore{items: []store.ReceiptDeletion{{ID: 5, Path: "a/x.jpg"}}, ackErr: errors.New("ack failed")}
	r := &fakeReceiptStorage{}
	processQueue(context.Background(), s, r)
	processQueue(context.Background(), s, r)
	if r.calls != 2 || len(s.acked) != 2 {
		t.Fatalf("calls=%d ack=%v", r.calls, s.acked)
	}
}
