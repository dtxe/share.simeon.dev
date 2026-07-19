package receipts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type MigrationStore interface {
	Head(context.Context, string) (ObjectInfo, error)
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}
type ObjectInfo struct {
	Size   int64
	SHA256 string
}
type MigrationResult struct{ Referenced, Uploaded, WouldUpload, Skipped, Verified, Orphans int }

// Migrate copies exactly the bytes already on disk. It deliberately refuses
// to replace an object whose identity cannot be established.
func Migrate(ctx context.Context, store MigrationStore, dir string, paths []string, dryRun bool) (MigrationResult, error) {
	refs, err := normalizedPaths(paths)
	if err != nil {
		return MigrationResult{}, err
	}
	var result MigrationResult
	result.Referenced = len(refs)
	for _, rel := range refs {
		full, err := localPath(dir, rel)
		if err != nil {
			return result, err
		}
		data, err := readLocalFile(dir, full)
		if err != nil {
			return result, fmt.Errorf("referenced local receipt is unavailable: %w", err)
		}
		h := sha256.Sum256(data)
		digest := hex.EncodeToString(h[:])
		if dryRun {
			obj, headErr := store.Head(ctx, rel)
			if headErr == nil {
				if obj.Size != int64(len(data)) || obj.SHA256 == "" || !strings.EqualFold(obj.SHA256, digest) {
					return result, fmt.Errorf("s3 conflict (size or sha256 differs)")
				}
				result.Skipped++
			} else if errors.Is(headErr, fs.ErrNotExist) {
				result.WouldUpload++
			} else {
				return result, fmt.Errorf("head referenced receipt: %w", headErr)
			}
			continue
		}
		obj, err := store.Head(ctx, rel)
		if err == nil {
			if obj.Size != int64(len(data)) || obj.SHA256 == "" || !strings.EqualFold(obj.SHA256, digest) {
				return result, fmt.Errorf("s3 conflict for referenced receipt (size or sha256 differs)")
			}
			result.Skipped++
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return result, fmt.Errorf("head referenced receipt: %w", err)
		}
		if err := store.Put(ctx, rel, bytes.NewReader(data), int64(len(data)), digest); err != nil {
			return result, err
		}
		result.Uploaded++
	}
	result.Orphans, err = reportOrphans(dir, refs)
	if err != nil {
		return result, err
	}
	return result, nil
}

func Verify(ctx context.Context, store MigrationStore, dir string, paths []string) (MigrationResult, error) {
	refs, err := normalizedPaths(paths)
	if err != nil {
		return MigrationResult{}, err
	}
	result := MigrationResult{Referenced: len(refs)}
	for _, rel := range refs {
		full, err := localPath(dir, rel)
		if err != nil {
			return result, err
		}
		local, err := openLocalFile(dir, full)
		if err != nil {
			return result, fmt.Errorf("referenced local receipt is unavailable: %w", err)
		}
		remote, err := store.Get(ctx, rel)
		if err != nil {
			local.Close()
			return result, fmt.Errorf("get referenced receipt: %w", err)
		}
		lh, rh := sha256.New(), sha256.New()
		_, le := io.Copy(lh, local)
		_, re := io.Copy(rh, remote)
		localErr, remoteErr := local.Close(), remote.Close()
		if le != nil {
			return result, le
		}
		if re != nil {
			return result, re
		}
		if localErr != nil {
			return result, localErr
		}
		if remoteErr != nil {
			return result, remoteErr
		}
		if !bytes.Equal(lh.Sum(nil), rh.Sum(nil)) {
			return result, fmt.Errorf("verification hash mismatch")
		}
		result.Verified++
	}
	result.Orphans, err = reportOrphans(dir, refs)
	return result, err
}

func normalizedPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		n, err := NormalizePath(p)
		if err != nil {
			return nil, fmt.Errorf("invalid persisted receipt path: %w", err)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}
func localPath(dir, rel string) (string, error) {
	n, err := NormalizePath(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(n)), nil
}

func openLocalFile(dir, full string) (*os.File, error) {
	if _, err := readLocalFile(dir, full); err != nil {
		return nil, err
	}
	return os.Open(full)
}
func readLocalFile(dir, full string) ([]byte, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("receipt resolves outside upload directory")
	}
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("referenced receipt is not a regular file")
	}
	return os.ReadFile(full)
}

func reportOrphans(dir string, refs []string) (int, error) {
	wanted := map[string]bool{}
	for _, p := range refs {
		n, _ := NormalizePath(p)
		wanted[n] = true
	}
	n := 0
	err := filepath.WalkDir(dir, func(path string, ent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ent.IsDir() {
			return nil
		}
		if ent.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid local relative path")
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if _, e = NormalizePath(rel); e != nil {
			return fmt.Errorf("invalid local relative path")
		}
		if !wanted[rel] {
			n++
		}
		return nil
	})
	return n, err
}

func RandomCanary() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "migration-canary/" + hex.EncodeToString(b) + ".jpg", nil
}

// S3MigrationStore adapts the AWS client and centralizes migration headers.
type S3MigrationStore struct {
	Client interface {
		HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
		PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
		GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
		DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	}
	Bucket, Prefix string
}

func (s *S3MigrationStore) key(path string) (string, error) {
	return ObjectKey(s.Prefix, path)
}
func ObjectKey(prefix, path string) (string, error) {
	n, err := NormalizePath(path)
	if err != nil {
		return "", err
	}
	p := strings.Trim(prefix, "/")
	if p == "" {
		return n, nil
	}
	return p + "/" + n, nil
}
func (s *S3MigrationStore) Head(ctx context.Context, path string) (ObjectInfo, error) {
	k, e := s.key(path)
	if e != nil {
		return ObjectInfo{}, s3OperationError("head", e)
	}
	o, e := s.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(k)})
	if e != nil {
		var a smithy.APIError
		if errors.As(e, &a) && (a.ErrorCode() == "NoSuchKey" || a.ErrorCode() == "NotFound" || a.ErrorCode() == "404") {
			return ObjectInfo{}, fs.ErrNotExist
		}
		return ObjectInfo{}, s3OperationError("head", e)
	}
	var size int64
	if o.ContentLength != nil {
		size = *o.ContentLength
	}
	return ObjectInfo{Size: size, SHA256: o.Metadata["sha256"]}, nil
}
func (s *S3MigrationStore) Put(ctx context.Context, path string, r io.Reader, size int64, digest string) error {
	k, e := s.key(path)
	if e != nil {
		return s3OperationError("put", e)
	}
	_, e = s.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(k), Body: r, ContentLength: aws.Int64(size), ContentType: aws.String("image/jpeg"), ContentDisposition: aws.String(`inline; filename="receipt.jpg"`), CacheControl: aws.String("private, max-age=3600"), Metadata: map[string]string{"sha256": digest}, ServerSideEncryption: types.ServerSideEncryptionAes256})
	if e != nil {
		return s3OperationError("put", e)
	}
	return nil
}
func (s *S3MigrationStore) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	k, e := s.key(path)
	if e != nil {
		return nil, s3OperationError("get", e)
	}
	o, e := s.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(k)})
	if e != nil {
		var a smithy.APIError
		if errors.As(e, &a) && (a.ErrorCode() == "NoSuchKey" || a.ErrorCode() == "NotFound" || a.ErrorCode() == "404") {
			return nil, fs.ErrNotExist
		}
		return nil, s3OperationError("get", e)
	}
	return o.Body, nil
}
func (s *S3MigrationStore) Delete(ctx context.Context, path string) error {
	k, e := s.key(path)
	if e != nil {
		return s3OperationError("delete", e)
	}
	_, e = s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(k)})
	if e != nil {
		return s3OperationError("delete", e)
	}
	return nil
}

func s3OperationError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("s3 %s failed: %w", operation, err)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("s3 %s failed with code %s", operation, apiErr.ErrorCode())
	}
	return fmt.Errorf("s3 %s failed", operation)
}
