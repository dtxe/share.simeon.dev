package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5/pgxpool"

	"share/backend/internal/config"
	"share/backend/internal/receipts"
)

type awsOps interface {
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
}
type presigner interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "preflight" && os.Args[1] != "migrate" && os.Args[1] != "verify") {
		log.Fatalf("usage: receipt-migrate preflight|migrate [--dry-run]|verify")
	}
	mode := os.Args[1]
	dry := false
	for _, a := range os.Args[2:] {
		if a == "--dry-run" {
			dry = true
		} else {
			log.Fatalf("unknown flag %q", a)
		}
	}
	if dry && mode != "migrate" {
		log.Fatal("--dry-run is only valid with migrate")
	}
	cfg, err := config.LoadMigration()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.S3Region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3Credentials.AccessKey, cfg.S3Credentials.SecretKey, "")), awsconfig.WithBaseEndpoint(cfg.S3Endpoint))
	if err != nil {
		log.Fatal(err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) { o.UsePathStyle = false })
	store := &receipts.S3MigrationStore{Client: client, Bucket: cfg.S3Bucket, Prefix: cfg.S3Prefix}
	if mode == "preflight" {
		if err := preflight(ctx, cfg, client, store, s3.NewPresignClient(client)); err != nil {
			log.Fatal(err)
		}
		return
	}
	if dry {
		log.Printf("dry-run: skipping mutating S3 preflight; no writes will be performed")
	} else if err := preflight(ctx, cfg, client, store, s3.NewPresignClient(client)); err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	paths, err := receiptPaths(ctx, pool)
	if err != nil {
		log.Fatal(err)
	}
	var result receipts.MigrationResult
	if mode == "migrate" {
		result, err = receipts.Migrate(ctx, store, cfg.UploadDir, paths, dry)
		if err == nil && !dry {
			verified, verifyErr := receipts.Verify(ctx, store, cfg.UploadDir, paths)
			if verifyErr != nil {
				err = verifyErr
			} else {
				result.Verified = verified.Verified
				result.Orphans = verified.Orphans
			}
		}
	} else {
		result, err = receipts.Verify(ctx, store, cfg.UploadDir, paths)
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("receipt migration %s: referenced=%d uploaded=%d would_upload=%d skipped=%d verified=%d orphans=%d dry_run=%t", mode, result.Referenced, result.Uploaded, result.WouldUpload, result.Skipped, result.Verified, result.Orphans, dry)
}

func receiptPaths(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT receipt_image_path FROM bill_sessions WHERE receipt_image_path IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

const cleanupTimeout = 10 * time.Second

func preflight(ctx context.Context, c *config.MigrationConfig, ops awsOps, store receipts.MigrationStore, signer presigner) error {
	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return preflightWithHTTP(ctx, c, ops, store, signer, noRedirect)
}

func preflightWithHTTP(ctx context.Context, c *config.MigrationConfig, ops awsOps, store receipts.MigrationStore, signer presigner, client httpDoer) (err error) {
	if _, err = ops.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &c.S3Bucket}); err != nil {
		return fmt.Errorf("S3 bucket authentication failed: %w", err)
	}

	var canary string
	canary, err = receipts.RandomCanary()
	if err != nil {
		return err
	}

	var cleanupFailed bool
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if delErr := store.Delete(cleanupCtx, canary); delErr != nil {
			cleanupFailed = true
		}
		if cleanupFailed {
			if err != nil {
				err = errors.Join(err, fmt.Errorf("canary cleanup failed"))
			} else {
				err = fmt.Errorf("canary cleanup failed")
			}
		}
	}()

	data := []byte("share receipt migration canary")
	h := sha256.Sum256(data)
	if err = store.Put(ctx, canary, bytes.NewReader(data), int64(len(data)), fmt.Sprintf("%x", h)); err != nil {
		return fmt.Errorf("canary write failed: %w", err)
	}

	r, err := store.Get(ctx, canary)
	if err != nil {
		return fmt.Errorf("canary read failed: %w", err)
	}
	got, getReadErr := io.ReadAll(r)
	getCloseErr := r.Close()
	if getReadErr != nil || getCloseErr != nil || !bytes.Equal(data, got) {
		return fmt.Errorf("canary read verification failed")
	}

	key, err := receipts.ObjectKey(c.S3Prefix, canary)
	if err != nil {
		return err
	}
	signed, err := signer.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &c.S3Bucket, Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("presigning failed: %w", err)
	}

	u, err := url.Parse(signed.URL)
	if err != nil {
		return fmt.Errorf("presigned URL invalid")
	}
	if err := validatePresignedURL(u, c.S3ProxyHost); err != nil {
		return err
	}

	presignedReq, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.URL, nil)
	if err != nil {
		return fmt.Errorf("presigned fetch request invalid")
	}
	presignedResp, err := client.Do(presignedReq)
	if err != nil {
		return fmt.Errorf("presigned canary fetch failed: %w", err)
	}
	presignedBytes, presignedReadErr := io.ReadAll(presignedResp.Body)
	presignedCloseErr := presignedResp.Body.Close()
	if presignedReadErr != nil || presignedCloseErr != nil || presignedResp.StatusCode < 200 || presignedResp.StatusCode >= 300 || !bytes.Equal(data, presignedBytes) {
		return fmt.Errorf("presigned canary verification failed")
	}

	unsigned := *u
	unsigned.RawQuery = ""
	unsigned.ForceQuery = false
	unsigned.User = nil
	unsigned.Fragment = ""
	unsigned.RawFragment = ""

	unsignedReq, err := http.NewRequestWithContext(ctx, http.MethodGet, unsigned.String(), nil)
	if err != nil {
		return fmt.Errorf("unsigned canary request invalid")
	}
	unsignedResp, err := client.Do(unsignedReq)
	if err != nil {
		return fmt.Errorf("unsigned canary fetch failed: %w", err)
	}
	unsignedCloseErr := unsignedResp.Body.Close()

	// 401/403/404 prove the object is not directly readable; 2xx proves it is;
	// redirects and other statuses are inconclusive.
	var unsignedErr error
	switch unsignedResp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// expected for a private object
	case http.StatusOK, http.StatusPartialContent, http.StatusNoContent:
		unsignedErr = fmt.Errorf("canary is publicly readable")
	default:
		unsignedErr = fmt.Errorf("unsigned canary fetch returned inconclusive status %d", unsignedResp.StatusCode)
	}
	if unsignedCloseErr != nil {
		unsignedErr = errors.Join(unsignedErr, fmt.Errorf("unsigned response body close failed"))
	}
	if unsignedErr != nil {
		return unsignedErr
	}

	if v, err := ops.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &c.S3Bucket}); err == nil {
		log.Printf("bucket versioning: %s", v.Status)
	} else {
		log.Printf("bucket versioning: unavailable")
	}
	log.Printf("preflight passed: authenticated bucket, canary verified, presign capability configured, unsigned canary not readable")
	return nil
}

func validatePresignedURL(u *url.URL, expectedHost string) error {
	if u == nil {
		return fmt.Errorf("presigned URL invalid")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("presigned URL must use HTTPS")
	}
	if u.Hostname() != expectedHost {
		return fmt.Errorf("presigned URL host mismatch")
	}
	if u.Port() != "" {
		return fmt.Errorf("presigned URL must not include a port")
	}
	if u.User != nil || u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("presigned URL must not contain user info or fragment")
	}
	return nil
}
