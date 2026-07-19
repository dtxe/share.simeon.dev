package receipts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type awsMigrationFake struct {
	put  *s3.PutObjectInput
	body []byte
}

func (f *awsMigrationFake) HeadObject(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return nil, fs.ErrNotExist
}
func (f *awsMigrationFake) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.put = in
	f.body, _ = io.ReadAll(in.Body)
	return &s3.PutObjectOutput{}, nil
}
func (f *awsMigrationFake) GetObject(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}
func (f *awsMigrationFake) DeleteObject(_ context.Context, _ *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return &s3.DeleteObjectOutput{}, nil
}

type migrationObject struct {
	data []byte
	sha  string
}
type memoryMigrationStore struct {
	objects              map[string]migrationObject
	heads, puts, deletes int
}

func newMemoryStore() *memoryMigrationStore {
	return &memoryMigrationStore{objects: map[string]migrationObject{}}
}
func (s *memoryMigrationStore) Head(_ context.Context, p string) (ObjectInfo, error) {
	s.heads++
	o, ok := s.objects[p]
	if !ok {
		return ObjectInfo{}, fs.ErrNotExist
	}
	return ObjectInfo{Size: int64(len(o.data)), SHA256: o.sha}, nil
}
func (s *memoryMigrationStore) Put(_ context.Context, p string, r io.Reader, _ int64, sha string) error {
	s.puts++
	b, _ := io.ReadAll(r)
	s.objects[p] = migrationObject{b, sha}
	return nil
}
func (s *memoryMigrationStore) Get(_ context.Context, p string) (io.ReadCloser, error) {
	o, ok := s.objects[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(o.data)), nil
}
func (s *memoryMigrationStore) Delete(_ context.Context, p string) error {
	s.deletes++
	delete(s.objects, p)
	return nil
}
func writeReceipt(t *testing.T, dir, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateUploadSkipConflictAndHeadersInput(t *testing.T) {
	dir := t.TempDir()
	data := []byte("jpeg bytes")
	writeReceipt(t, dir, "session/file.jpg", data)
	s := newMemoryStore()
	r, err := Migrate(context.Background(), s, dir, []string{"session/file.jpg", "session/file.jpg"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Uploaded != 1 || s.puts != 1 {
		t.Fatalf("result=%+v puts=%d", r, s.puts)
	}
	if !bytes.Equal(s.objects["session/file.jpg"].data, data) || s.objects["session/file.jpg"].sha == "" {
		t.Fatal("exact bytes/hash not stored")
	}
	r, err = Migrate(context.Background(), s, dir, []string{"session/file.jpg"}, false)
	if err != nil || r.Skipped != 1 || s.puts != 1 {
		t.Fatalf("skip result=%+v err=%v", r, err)
	}

	// Existing object with a mismatching SHA256 must be treated as a conflict.
	s.objects["session/file.jpg"] = migrationObject{data: data, sha: "wrong"}
	r, err = Migrate(context.Background(), s, dir, []string{"session/file.jpg"}, false)
	if err == nil || s.puts != 1 {
		t.Fatalf("expected sha conflict, puts=%d err=%v", s.puts, err)
	}

	// Existing object with no SHA256 metadata is also unverifiable and must conflict.
	s.objects["session/file.jpg"] = migrationObject{data: data, sha: ""}
	r, err = Migrate(context.Background(), s, dir, []string{"session/file.jpg"}, false)
	if err == nil || s.puts != 1 {
		t.Fatalf("expected missing-sha conflict, puts=%d err=%v", s.puts, err)
	}

	// Existing object whose size differs is a conflict even if SHA is absent.
	s.objects["session/file.jpg"] = migrationObject{data: append(data, 0), sha: ""}
	r, err = Migrate(context.Background(), s, dir, []string{"session/file.jpg"}, false)
	if err == nil || s.puts != 1 {
		t.Fatalf("expected size conflict, puts=%d err=%v", s.puts, err)
	}
}

func TestS3MigrationStoreUsesExactKeyAndHeaders(t *testing.T) {
	f := &awsMigrationFake{}
	s := &S3MigrationStore{Client: f, Bucket: "bucket", Prefix: "receipts"}
	data := []byte("jpeg bytes")
	h := sha256.Sum256(data)
	if err := s.Put(context.Background(), "session/file.jpg", bytes.NewReader(data), int64(len(data)), hex.EncodeToString(h[:])); err != nil {
		t.Fatal(err)
	}
	if *f.put.Key != "receipts/session/file.jpg" || *f.put.ContentType != "image/jpeg" || *f.put.ContentDisposition != `inline; filename="receipt.jpg"` || *f.put.CacheControl != "private, max-age=3600" || f.put.Metadata["sha256"] != hex.EncodeToString(h[:]) || f.put.ServerSideEncryption != types.ServerSideEncryptionAes256 {
		t.Fatalf("unexpected put: %#v", f.put)
	}
}

func TestMigrateMissingDryRunOrphansAndInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	s := newMemoryStore()
	if _, err := Migrate(context.Background(), s, dir, []string{"session/missing.jpg"}, false); err == nil {
		t.Fatal("missing local receipt accepted")
	}
	writeReceipt(t, dir, "session/file.jpg", []byte("data"))
	writeReceipt(t, dir, "session/orphan.jpg", []byte("orphan"))
	r, err := Migrate(context.Background(), s, dir, []string{"session/file.jpg"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Uploaded != 0 || r.WouldUpload != 1 || r.Orphans != 1 || s.puts != 0 {
		t.Fatalf("dry run result=%+v puts=%d", r, s.puts)
	}
	if _, err := Migrate(context.Background(), s, dir, []string{"../escape.jpg"}, true); err == nil {
		t.Fatal("unsafe path accepted")
	}
}

func TestVerifyMismatchAndSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	writeReceipt(t, dir, "session/file.jpg", []byte("local"))
	s := newMemoryStore()
	s.objects["session/file.jpg"] = migrationObject{data: []byte("remote"), sha: ""}
	if _, err := Verify(context.Background(), s, dir, []string{"session/file.jpg"}); err == nil {
		t.Fatal("hash mismatch accepted")
	}
	out := filepath.Join(t.TempDir(), "outside.jpg")
	if err := os.WriteFile(out, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "session/file.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(dir, "session/file.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), s, dir, []string{"session/file.jpg"}, false); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestObjectKeyAndCanary(t *testing.T) {
	p, err := RandomCanary()
	if err != nil {
		t.Fatal(err)
	}
	if k, err := ObjectKey("/receipts/", p); err != nil || k != "receipts/"+p {
		t.Fatalf("key=%q err=%v", k, err)
	}
	if k, err := ObjectKey("", p); err != nil || k != p {
		t.Fatalf("empty prefix key=%q err=%v", k, err)
	}
}

// AWS S3 and OVH can return several codes for a missing object; all must map
// to fs.ErrNotExist so the caller treats them as "needs upload" rather than a
// hard S3 failure.
func TestS3MigrationStoreHeadMissingErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"NoSuchKey", "NoSuchKey"},
		{"NotFound", "NotFound"},
		{"404", "404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &missingObjectHeadFake{code: tc.code}
			s := &S3MigrationStore{Client: client, Bucket: "bucket", Prefix: "receipts"}
			_, err := s.Head(context.Background(), "session/file.jpg")
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("expected fs.ErrNotExist, got %v", err)
			}
		})
	}
}

type missingObjectHeadFake struct{ code string }

func (f *missingObjectHeadFake) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return nil, &smithy.GenericAPIError{Code: f.code, Message: "not found"}
}
func (f *missingObjectHeadFake) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return &s3.PutObjectOutput{}, nil
}
func (f *missingObjectHeadFake) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{}, nil
}
func (f *missingObjectHeadFake) DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return &s3.DeleteObjectOutput{}, nil
}
