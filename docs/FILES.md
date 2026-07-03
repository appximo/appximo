# File store (FILES-V2) — interchangeable backends: local disk + any S3-compatible

The engine ships a multi-tenant file store on `/api/files` with **two storage
backends selected by config**: `local` (everything on your VPS's disk — the
BYOC minimum-cost default) and `s3` (any S3-compatible provider: Cloudflare
R2, DigitalOcean Spaces, self-hosted MinIO, AWS S3). **Tenancy, RBAC, metadata
and upload validation are identical on both** — the same conformance test
suite gates both drivers (`pkg/files/backend_conformance_test.go`, run against
real MinIO in the integration lane).

## Architecture — the PocketBase pattern, split correctly

```
   HTTP handlers (upload / download / signed URL / delete)
        │  tenant (Host) → JWT → RBAC("files") — the engine's normal chain
        ▼
   Store  (pkg/files/store.go)          ← identical on every backend
        │  · metadata AUTHORITATIVE in Postgres (tenant_<id>.files)
        │  · OWASP upload validation (allowlist + magic bytes + sanitize)
        │  · SHA-256 streaming hash, dedup, tenant-scoped ids
        ▼
   Backend (pkg/files/backend.go)       ← thin, swappable byte mover
      ├─ LocalBackend   disk, http.ServeContent (Range/ETag/sendfile)
      └─ S3Backend      gocloud.dev s3blob → R2/Spaces/MinIO/AWS by config
```

- **Keys** are always `<tenant>/<aa>/<bb>/<sha256>`: content-addressed (dedup is free within a tenant; two uploads of the same
  bytes are two ids over one blob), tenant-prefixed (physical isolation), and
  built ONLY from a validated tenant id + hash hex — client input never touches
  a storage path, so traversal is structurally impossible. Every driver
  re-validates keys at its boundary. On S3 the keys live under a bucket prefix
  (default `tenants/`).
- **Metadata lives in the engine's Postgres** (id, sha256, size, content type,
  sanitized original name, created_at — per-tenant table), never in the bucket:
  queries and RBAC never depend on bucket List calls (the Supabase/PocketBase
  pattern).
- The local driver is a **direct-disk implementation, not gocloud fileblob**,
  deliberately: the storage investigation measured that the local ceiling IS
  the standard library (`http.ServeContent` over an `*os.File` → sendfile
  zero-copy, ~24× less CPU than a manual copy path), and wrapping the file in a
  portability layer would break the `*os.File` fast path while adding nothing —
  FILES-V1's atomic temp+rename CAS already was the owned implementation
  PocketBase eventually migrated to. gocloud earns its weight only on the S3
  side (one portable driver for every provider + automatic multipart uploads).

## Serving: the 302-vs-proxy reconciliation

The FILES-V1 contract said "S3 = presigned URL + 302 — the engine authorizes
but never proxies the bytes". PocketBase proxies everything through Go instead
(uniform RBAC, bucket never exposed). Both are right for different backends;
FILES-V2 reconciles per driver:

| Backend | `GET /api/files/{id}` | Why |
|---|---|---|
| `local` | **proxy** — `http.ServeContent`: 200/206 Range, strong ETag (the content hash) → 304 revalidation, `sendfile` zero-copy | proxying is the only option AND the optimum: the standard library gives Range + conditional + zero-copy for free; the limit is the disk/NIC, never Go |
| `s3` (default: `redirect`) | **302** to a short-lived presigned URL (Signature v4, same TTL as tokens) | keeps the FILES-V1 contract: the engine authorizes (tenant → JWT → RBAC ran before the redirect) and then gets out of the byte path — zero engine bandwidth, egress straight from the bucket (with R2 that egress is **$0**) |
| `s3` with `APPITOOLS_FILES_S3_SERVE=proxy` | **proxy** — ServeContent over a lazy-seeking bucket reader (ranged GETs under the hood) | for buckets that must stay fully private / clients that can't follow redirects; the PocketBase trade-off: uniform headers, bytes transit the engine |

Objects are uploaded to S3 **with** their validated `Content-Type` and an
`attachment` `Content-Disposition`, so a presigned GET replays safe headers
without the engine in the path.

## Signed URLs — protected files without a Bearer header

`GET /api/files/{id}/url` (authenticated: RBAC `read` on `files`) returns
`{"url", "expires_in"}` — a short-lived link usable by `<img>`/`<video>` tags,
share links, or anything that can't send `Authorization`:

- **Local backend**: an engine-minted HS256 token URL,
  `GET /files/signed/{token}`. The token (~TTL 180 s, configurable) carries
  `{tenant, file_id, role}` with claim keys disjoint from the access token's
  (presented to `/api/*` it has no role → RBAC denies). At serve time the
  engine re-verifies signature + expiry, that the token's tenant matches the
  request's Host tenant, and that the embedded role STILL has `read` on
  `files`. **Every failure is a uniform 404** — an invalid token is
  indistinguishable from a missing file (anti-fingerprinting).
- **S3 backend**: a native presigned URL (Signature v4), same short TTL; the
  bucket serves the bytes directly.

## Upload validation (OWASP)

Applied inside `Store.Put` — every ingestion path, not just HTTP; a rejected
upload is a `422` with the reason:

1. **Extension allowlist** (never a denylist): default list in
   `files.DefaultAllowedExtensions` (images/docs/office/archives/media/fonts +
   `bin`); override with `APPITOOLS_FILES_ALLOWED_EXT` (comma-separated; `*`
   disables the extension check only). `.php`, `.exe`, `.sh`… are simply not
   representable. A file with no extension is accepted (hash-keyed, served
   `attachment` + `nosniff` — inert).
2. **Magic bytes** (`http.DetectContentType`, first 512 bytes — the client
   Content-Type is never trusted): a well-known binary extension whose content
   conclusively sniffs as something else is rejected (PHP-in-a-.jpg dies here),
   and a DECLARED image/video/audio/pdf/zip type whose content sniffs outside
   that family is rejected regardless of extension. An inconclusive sniff
   (`application/octet-stream`) never rejects. SVG is exempt from the family
   check (it has no sniff signature — and is served `attachment`+`nosniff`, so
   it never executes in-origin).
3. **Size cap**: `APPITOOLS_FILES_MAX_BYTES` (default 256 MiB) → `413`.
4. **Name sanitization at rest**: the client filename is stored as metadata
   only — basename, control/quote characters stripped, no leading dots, `..`
   collapsed, 200-rune cap. Storage keys are content hashes; the name never
   builds a path.
5. **Streaming, bounded RAM**: uploads stream through a 64 KiB buffer to a
   staging file (hash computed on the way), then commit atomically (local:
   same-filesystem rename; S3: multipart via the SDK transfer manager). An
   interrupted upload leaves nothing behind.

## Routes

| Route | RBAC (`files` resource) | Notes |
|---|---|---|
| `POST /api/files` (multipart, field `file`) | `create` | `201 {file_id, sha256, size}`; `422` on a rejected upload; `413` over the cap |
| `GET /api/files/{id}` | `read` | streams (local/proxy) or 302s (S3 redirect); Range/ETag honored; response-cache bypassed |
| `GET /api/files/{id}/url` | `read` | `200 {url, expires_in}` — signed, short-lived |
| `DELETE /api/files/{id}` | `delete` | `204`; blob removed only when no other upload references the same content |
| `GET /files/signed/{token}` | token IS the credential | JWT-skipped; token verified (signature/expiry/tenant/role) then served like a download; any failure → `404` |

All `/api/files*` routes flow through the engine's normal chain (tenant Host →
rate limit → JWT → RBAC deny-by-default): a role needs the `files` resource in
its policy. File ids are tenant-scoped (no cross-tenant handle) and blobs are
tenant-prefixed in storage.

## Operator config

| Env (Config field) | Default | Meaning |
|---|---|---|
| `APPITOOLS_FILES_BACKEND` | `local` | `local` \| `s3` |
| `APPITOOLS_FILES_DIR` | `/var/lib/appitools/files` | local backend root (lazy-created) |
| `APPITOOLS_FILES_MAX_BYTES` | 268435456 (256 MiB) | per-upload cap |
| `APPITOOLS_FILES_TOKEN_TTL` | `180` (seconds) | signed URL/token lifetime (engine tokens AND S3 presigned) |
| `APPITOOLS_FILES_ALLOWED_EXT` | curated allowlist | comma-separated extensions, or `*` |
| `APPITOOLS_FILES_S3_BUCKET` | — | **required** with backend=s3 (boot fails loud without it) |
| `APPITOOLS_FILES_S3_ENDPOINT` | AWS default | provider endpoint URL |
| `APPITOOLS_FILES_S3_REGION` | `auto` | R2's spelling; harmless elsewhere |
| `APPITOOLS_FILES_S3_ACCESS_KEY` / `_SECRET_KEY` | — | **required** with backend=s3 |
| `APPITOOLS_FILES_S3_FORCE_PATH_STYLE` | off | `<endpoint>/<bucket>` addressing — **required for MinIO** |
| `APPITOOLS_FILES_S3_PREFIX` | `tenants/` | key namespace inside the bucket |
| `APPITOOLS_FILES_S3_SERVE` | `redirect` | `redirect` \| `proxy` (see reconciliation above) |

### Setup 1 — local (the default; everything on your VPS)

```bash
# nothing to configure; optionally pin the root:
APPITOOLS_FILES_DIR=/var/lib/appitools/files
```
Cost: your disk. Serving: Range/ETag/sendfile, the local ceiling. Back-compat:
the on-disk layout is byte-identical to FILES-V1 — existing blobs keep working.

### Setup 2 — Cloudflare R2 (the recommended S3 default)

Storage $0.015/GB-month, **egress $0 always**, free tier 10 GB, PoPs in
Bogotá/Medellín/Cali/Barranquilla/São Paulo. Create a bucket + an R2 API token
(Object Read & Write), then:

```bash
APPITOOLS_FILES_BACKEND=s3
APPITOOLS_FILES_S3_BUCKET=my-app-files
APPITOOLS_FILES_S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
APPITOOLS_FILES_S3_REGION=auto
APPITOOLS_FILES_S3_ACCESS_KEY=…
APPITOOLS_FILES_S3_SECRET_KEY=…
```
Known caveat: R2 has had occasional S3-compat edge bugs (e.g. a `Size() 0` on
some JSON/SVG objects was reported historically). The engine's compat gate is
the MinIO conformance suite; if you hit an R2 edge, `proxy` serve mode is the
workaround (the engine's reader path avoids presigned-GET quirks).

### Setup 3 — self-hosted MinIO (S3 on your own box)

```bash
docker run -d --name minio -p 9000:9000 \
  -e MINIO_ROOT_USER=… -e MINIO_ROOT_PASSWORD=… \
  -v /var/lib/minio:/data minio/minio server /data

APPITOOLS_FILES_BACKEND=s3
APPITOOLS_FILES_S3_BUCKET=appitools
APPITOOLS_FILES_S3_ENDPOINT=http://127.0.0.1:9000
APPITOOLS_FILES_S3_REGION=us-east-1
APPITOOLS_FILES_S3_ACCESS_KEY=…
APPITOOLS_FILES_S3_SECRET_KEY=…
APPITOOLS_FILES_S3_FORCE_PATH_STYLE=true
```
This exact setup is what the integration suite runs
(`go test -tags integration -run TestS3 ./pkg/files/`). Note: with a
localhost-only MinIO, presigned URLs point at the internal endpoint — use
`APPITOOLS_FILES_S3_SERVE=proxy`, or give MinIO a public endpoint.

DigitalOcean Spaces works with the same five variables ($5/mo flat: 250 GiB +
1 TiB egress; endpoint `https://<region>.digitaloceanspaces.com`). AWS S3:
leave the endpoint empty, set a real region. **Switching provider is a config
change** — no code path differs.

## What consumers see

- The XLSX worker and any `pkg/files.VFS` consumer are unchanged: `file_ref`
  is still a tenant-scoped `file_id`, resolved via `VFS.Get` on whatever
  backend the process configured.
- The FILES-V1 public contract (`POST /api/files`, `GET /api/files/{id}`,
  upload response shape, RBAC actions, 404 semantics) is preserved. New,
  additive: `DELETE`, `/url`, `/files/signed/{token}`, Range/ETag on
  downloads, and the 422 upload rejections (previously invalid uploads were
  accepted — tightening documented here and in the changelog).
