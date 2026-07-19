package receipts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"io/fs"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type fakeS3 struct {
	put     *s3.PutObjectInput
	body    []byte
	deleted string
	missing bool
}

func (f *fakeS3) PutObject(ctx context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.put = in
	f.body, _ = io.ReadAll(in.Body)
	return &s3.PutObjectOutput{}, nil
}
func (f *fakeS3) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.missing {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "not found"}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}
func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.deleted = *in.Key
	return &s3.DeleteObjectOutput{}, nil
}

func TestS3SaveOpenDelete(t *testing.T) {
	f := &fakeS3{}
	storage := NewS3(f, nil, "share-app", "receipts")
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.Black)
	if err := jpeg.Encode(&raw, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	path, err := storage.Save(context.Background(), "session", bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(f.body)
	if path == "" || *f.put.Key != "receipts/"+path || *f.put.ContentType != "image/jpeg" || *f.put.ContentDisposition != `inline; filename="receipt.jpg"` || *f.put.CacheControl != "private, max-age=3600" || f.put.Metadata["sha256"] != hex.EncodeToString(h[:]) || f.put.ServerSideEncryption != types.ServerSideEncryptionAes256 {
		t.Fatalf("unexpected put: %#v", f.put)
	}
	got, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if b, _ := io.ReadAll(got); len(b) == 0 {
		t.Fatal("empty object")
	}
	if err := storage.Delete(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if f.deleted != "receipts/"+path {
		t.Fatalf("deleted key %q", f.deleted)
	}
}

func TestS3MissingObjectIsNotExist(t *testing.T) {
	f := &fakeS3{missing: true}
	_, err := NewS3(f, nil, "bucket", "receipts").Open(context.Background(), "session/file.jpg")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
}

func TestS3RejectsInvalidPathAndPresignTTL(t *testing.T) {
	s := NewS3(&fakeS3{}, nil, "bucket", "receipts")
	if _, err := s.Open(context.Background(), "../escape.jpg"); err == nil {
		t.Fatal("expected invalid path")
	}
	if _, err := s.PresignGet(context.Background(), "session/file.jpg", 0); err == nil {
		t.Fatal("expected invalid ttl")
	}
}

func TestS3CompressReuploadsSameKey(t *testing.T) {
	f := &fakeS3{}
	s := NewS3(f, nil, "bucket", "receipts")
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	if err := jpeg.Encode(&raw, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	path, err := s.Save(context.Background(), "session", bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Compress(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if *f.put.Key != "receipts/"+path || *f.put.ContentType != "image/jpeg" || f.put.Metadata["sha256"] == "" {
		t.Fatalf("compression did not upload normalized object: %#v", f.put)
	}
}
