package receipts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"golang.org/x/image/draw"
)

type s3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}
type s3Presigner interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignHeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type S3Storage struct {
	client         s3Client
	presigner      s3Presigner
	Bucket, Prefix string
	normalizer     Normalizer
}

func NewS3(client s3Client, presigner s3Presigner, bucket, prefix string, normalizer Normalizer) *S3Storage {
	if normalizer == nil {
		panic("receipts: nil normalizer")
	}
	return &S3Storage{client: client, presigner: presigner, Bucket: bucket, Prefix: cleanPrefix(prefix), normalizer: normalizer}
}
func cleanPrefix(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}
func (s *S3Storage) key(path string) (string, error) {
	p, err := NormalizePath(path)
	if err != nil {
		return "", err
	}
	return s.Prefix + p, nil
}

func (s *S3Storage) Save(ctx context.Context, sessionID string, r io.Reader) (string, error) {
	data, err := s.normalizer.Normalize(ctx, r)
	if err != nil {
		return "", err
	}
	name, err := randomFilename()
	if err != nil {
		return "", err
	}
	path := sessionID + "/" + name
	key, err := s.key(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key), Body: bytes.NewReader(data), ContentType: aws.String("image/jpeg"), ContentDisposition: aws.String(`inline; filename="receipt.jpg"`), CacheControl: aws.String("private, max-age=3600"), Metadata: map[string]string{"sha256": hex.EncodeToString(h[:])}, ServerSideEncryption: types.ServerSideEncryptionAes256})
	if err != nil {
		return "", fmt.Errorf("receipts: storing in s3: %w", err)
	}
	return path, nil
}
func (s *S3Storage) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	key, err := s.key(path)
	if err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
			return nil, fmt.Errorf("receipts: getting s3 object: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("receipts: getting s3 object: %w", err)
	}
	return out.Body, nil
}
func (s *S3Storage) Delete(ctx context.Context, path string) error {
	key, err := s.key(path)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("receipts: deleting s3 object: %w", err)
	}
	return nil
}
func (s *S3Storage) PresignGet(ctx context.Context, path string, ttl time.Duration) (string, error) {
	if s.presigner == nil {
		return "", fmt.Errorf("receipts: presigning unavailable")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("receipts: presign ttl must be positive")
	}
	key, err := s.key(path)
	if err != nil {
		return "", err
	}
	out, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)}, func(o *s3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
func (s *S3Storage) PresignHead(ctx context.Context, path string, ttl time.Duration) (string, error) {
	if s.presigner == nil {
		return "", fmt.Errorf("receipts: presigning unavailable")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("receipts: presign ttl must be positive")
	}
	key, err := s.key(path)
	if err != nil {
		return "", err
	}
	out, err := s.presigner.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)}, func(o *s3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

func (s *S3Storage) Compress(ctx context.Context, path string) (string, int, int, error) {
	f, err := s.Open(ctx, path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", 0, 0, err
	}
	b := img.Bounds()
	out := image.Image(img)
	if b.Dx() > postLLMMaxSide || b.Dy() > postLLMMaxSide {
		w, h := scaledDimensions(b.Dx(), b.Dy(), postLLMMaxSide)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		out = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: postLLMQuality}); err != nil {
		return "", 0, 0, err
	}
	clean, err := NormalizePath(path)
	if err != nil {
		return "", 0, 0, err
	}
	parts := strings.Split(clean, "/")
	name, err := randomFilename()
	if err != nil {
		return "", 0, 0, err
	}
	newPath := parts[0] + "/" + name
	key, err := s.key(newPath)
	if err != nil {
		return "", 0, 0, err
	}
	h := sha256.Sum256(buf.Bytes())
	_, err = s.client.PutObject(ctx, imagePut(s.Bucket, key, buf.Bytes(), h))
	if err != nil {
		return "", 0, 0, fmt.Errorf("receipts: storing compressed s3 object: %w", err)
	}
	return newPath, out.Bounds().Dx(), out.Bounds().Dy(), nil
}

func imagePut(bucket, key string, data []byte, h [32]byte) *s3.PutObjectInput {
	return &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data), ContentType: aws.String("image/jpeg"), ContentDisposition: aws.String(`inline; filename="receipt.jpg"`), CacheControl: aws.String("private, max-age=3600"), Metadata: map[string]string{"sha256": hex.EncodeToString(h[:])}, ServerSideEncryption: types.ServerSideEncryptionAes256}
}
