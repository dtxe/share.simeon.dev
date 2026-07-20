package httpapi

import (
	"context"
	"errors"
	"io"
	"testing"
)

type compensationStorage struct {
	deleteErr   error
	calls       int
	sawDeadline bool
}

func (f *compensationStorage) Save(context.Context, string, io.Reader) (string, error) {
	return "", nil
}
func (f *compensationStorage) Open(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (f *compensationStorage) Delete(ctx context.Context, _ string) error {
	f.calls++
	_, f.sawDeadline = ctx.Deadline()
	return f.deleteErr
}
func (f *compensationStorage) Compress(context.Context, string) (string, int, int, error) {
	return "", 0, 0, nil
}

func TestCompensateReceiptDeleteSuccessDoesNotEnqueue(t *testing.T) {
	r := &compensationStorage{}
	enqueued := 0
	if err := compensateReceipt(context.Background(), r, func(context.Context, string) error { enqueued++; return nil }, "x/a.jpg"); err != nil || enqueued != 0 || !r.sawDeadline {
		t.Fatalf("err=%v enqueued=%d deadline=%v", err, enqueued, r.sawDeadline)
	}
}

func TestCompensateReceiptDeleteFailureUsesFreshEnqueueContext(t *testing.T) {
	r := &compensationStorage{deleteErr: context.DeadlineExceeded}
	enqueued := 0
	if err := compensateReceipt(context.Background(), r, func(ctx context.Context, _ string) error {
		enqueued++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("enqueue context is unbounded")
		}
		return nil
	}, "x/a.jpg"); err != nil || enqueued != 1 {
		t.Fatalf("err=%v enqueued=%d", err, enqueued)
	}
}

func TestCompensateReceiptEnqueueFailureIsReturned(t *testing.T) {
	r := &compensationStorage{deleteErr: errors.New("delete")}
	want := errors.New("enqueue")
	if err := compensateReceipt(context.Background(), r, func(context.Context, string) error { return want }, "x/a.jpg"); err == nil || !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
