# Receipt storage migration runbook

This runbook covers the one-time migration from the local upload volume to the
private OVH S3 bucket, and the production cutover. It describes the code in
`b4bfd20`, `3469d3a`, `6f3d9dd`, and `a31f399`; it does not contain credentials.

## Storage contract and configuration

The production target is:

- bucket: `share-app`
- S3 API endpoint: `https://s3.bhs.io.cloud.ovh.net`
- region: `bhs`
- virtual-hosted bucket host: `share-app.s3.bhs.io.cloud.ovh.net`
- object prefix: `receipts/`

The bucket is private. Clients never receive a durable public object URL and
the bucket must not allow anonymous reads. The backend uses AWS-compatible S3
operations and virtual-hosted addressing (`UsePathStyle = false`). S3 upload
objects are JPEGs with private cache control, server-side AES256 encryption,
and a `sha256` metadata value.

The exact credentials document is one JSON object with exactly these non-empty
keys (case-sensitive):

```json
{"userName":"<OVH username>","accessKey":"<OVH access key>","secretKey":"<OVH secret key>"}
```

For Compose, create the local, gitignored secret file
`./secrets/s3_credentials.json`. Compose mounts it at
`/run/secrets/s3_credentials` and sets `S3_CREDENTIALS_FILE` for both
`backend` and the `receipt-migrate` ops service. Never put real values in this
document, `.env.example`, shell history, or a commit.

`RECEIPT_STORAGE` defaults to `s3`; set it to `local` only for development and
tests. Production receipt requests pass through Caddy's normal API proxy to the
Go backend, which authorizes the request and reads the private S3 object.
With `RECEIPT_STORAGE=s3`, these values are required:
`S3_ENDPOINT` (absolute HTTPS URL), `S3_BUCKET`, `S3_REGION`, `S3_PREFIX`,
`S3_PROXY_HOST` (hostname only), and `S3_CREDENTIALS` or
`S3_CREDENTIALS_FILE`. `UPLOAD_DIR` defaults to `/data/uploads` and remains
the local source/compatibility volume. Local mode does not require S3
credentials. The migration command independently requires database settings
(`DATABASE_URL`, or the `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD[_FILE]`/
`DB_NAME` combination) plus the S3 settings and credentials.

## Request path and privacy model

Existing client URLs remain unchanged:

- owner: `GET`/`HEAD /api/sessions/{id}/receipt`
- public share: `GET`/`HEAD /api/view/{token}/receipt`

These URLs use Caddy's normal `/api/*` proxy. The owner endpoint requires the
existing authenticated session and checks bill ownership; the public endpoint
requires the existing unguessable view token. Only after authorization does
the Go backend look up the database path and fetch the private object using
server-side S3 credentials. Neither an object URL nor S3 credentials are sent
to the browser. Caddy also sends `Referrer-Policy: no-referrer`.

Keep the bucket policy private and do not add CORS merely for this design:
browser requests are same-origin to Caddy. Direct unsigned bucket reads must
fail; the backend/Caddy path is the only client path.

## Preflight and migration commands

Run all commands from the repository root with the production `.env` and
Compose secret files in place. The ops service mounts the uploads volume
read-only and runs the compiled `/receipt-migrate` binary.

```sh
docker compose --profile ops build receipt-migrate
docker compose --profile ops run --rm receipt-migrate preflight
docker compose --profile ops run --rm receipt-migrate migrate --dry-run
docker compose --profile ops run --rm receipt-migrate migrate
docker compose --profile ops run --rm receipt-migrate verify
```

`preflight` authenticates the bucket, writes and reads a random canary,
checks a presigned fetch through the configured host, checks that the unsigned
canary is not publicly readable, logs the bucket versioning status, and then
deletes the canary. A canary cleanup failure is an error. A versioning API
failure is reported as unavailable; it is not silently treated as a
versioning approval, so the operator must resolve that review separately.

`migrate --dry-run` reads only the DB-referenced local files, performs HEAD
checks, reports `would_upload`, skips writes, and reports local orphans. It
does not run the mutating canary preflight; run `preflight` separately.
`migrate` copies exactly the referenced local bytes and automatically performs
a full verification afterward. `verify` repeats preflight, compares SHA-256 of
every referenced local file with its S3 object, and reports orphans.

Migration is referenced-only: files on disk that are not referenced by a
`bill_sessions.receipt_image_path` row are not uploaded. Existing S3 objects
are never overwritten. A matching size plus matching `sha256` metadata is
skipped; a missing object is uploaded; an existing object with absent or
mismatched size/digest metadata is a conflict. Orphans are reported, not
deleted. All commands exit
non-zero on invalid paths, missing referenced files, S3 conflicts, failed
verification, failed preflight, or other operational errors; a successful
command prints counts such as referenced, uploaded/would-upload, skipped,
verified, and orphans.

## Maintenance-window sequence

1. Announce the window and build the migration image before stopping traffic:
   `docker compose --profile ops build receipt-migrate`.
2. Stop frontend and backend services while keeping Postgres available. Do
   not run migration concurrently with uploads or extraction/compression jobs.
3. Back up Postgres and the uploads volume (including a restorable copy of
   `/data/uploads`). Record where both backups can be retrieved.
4. Run `preflight`, then `migrate --dry-run`; investigate every orphan and any
   non-zero exit before continuing.
5. Run `migrate`, then run the explicit full `verify` command and retain its
   output. Do not proceed if verification or conflict checks fail.
6. Deploy the S3-configured backend and frontend/Caddy image. Confirm receipt
   URLs pass through the normal `/api/*` backend proxy.
7. Run the smoke tests below, including unauthorized and direct-unsigned
   access tests.
8. Resume frontend/backend writes and monitor logs, cleanup, and the deletion
   queue through the first normal cleanup sweep.

## Cutover smoke tests and lifecycle checks

Using a real owner session and a public share token, check both receipt URLs
return the expected JPEG through the application host. Also check that a
different/absent owner session cannot fetch the owner URL, that a malformed
receipt path is rejected, and that an unsigned request to the OVH virtual
host/object returns 401/403/404 rather than bytes. Do not paste cookies or
signed URLs into tickets or logs.

Exercise a new upload, extraction, post-extraction compression, replacement
upload, and an expiry cleanup. Confirm the compressed replacement is served,
the old object is eventually deleted, and failed deletes remain queued for
retry. `receipt_deletion_queue` is durable: cleanup claims due rows in
batches, deletes with a bounded context, acknowledges success, and applies
exponential backoff (capped at 24 hours) after failures. Upload replacement,
compression races, and compensation failures also use this queue; an object
is not discarded merely because a delete request timed out.

Review bucket settings explicitly: no CORS is needed; public reads are
prohibited; versioning and noncurrent-version lifecycle rules must be
consciously configured and recorded. Do **not** apply an age rule that expires
current receipt objects: bill expiry is independent and can be extended.

## Rollback and local-volume retention

Production routes receipt bytes through the Go API path. This preserves the
private bucket and server-side authorization while avoiding proxy-layer
changes to signed S3 requests.

Before cutover, agree and record a local-volume review window (for example,
seven days) during which the backed-up/local `uploads` volume is retained;
that example is an operational recommendation, not an already-approved
mandatory retention policy. Do not delete it until the owner signs off.
After new S3 uploads exist, switching the backend back to local storage cannot
serve those new objects and is therefore not a complete rollback. A local
rollback requires preserving/rerouting existing S3 objects or restoring a
consistent backup; never assume the old volume alone contains post-cutover
data.
