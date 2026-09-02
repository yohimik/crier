# Staging

Instagram, Threads and TikTok's photo flow will not accept file uploads. They require a **public URL** and fetch the file themselves from their own servers. Staging is how a rendered file gets this URL.

Every other platform accepts raw bytes. You do not need staging if your project only publishes to those platforms.

```yaml
stage:
  mode: none      # none, s3, server or url
```

If you enable a platform that needs a URL with `stage.mode: none`, crier refuses to run before rendering anything:

```
crier: instagram can only be given a URL for the media, and stage.mode is none;
set stage.mode to s3, server or url
```

This results in exit code 1.

## `s3`: an object store

This is the most reliable option. You can use any S3-compatible endpoint: AWS, MinIO, Cloudflare R2, Backblaze B2, or DigitalOcean Spaces.

```yaml
stage:
  mode: s3
  s3:
    endpoint: s3.eu-west-1.amazonaws.com
    region: eu-west-1
    bucket: crier-media
    prefix: cards
    presign: true              # hand out a signed URL rather than a public one
    presign-expiry: 1h
    delete-after: true         # remove the object once publishing is done
```

```sh
export CRIER_STAGE_S3_ACCESS_KEY="…"
export CRIER_STAGE_S3_SECRET_KEY="…"
```

You can write the endpoint with a scheme, like `https://minio.example`. The scheme then decides the `use-ssl` setting.

**Presigned or public.** The default is `presign: true`, which works on a private bucket. If you use `presign: false`, the object needs to be publicly readable. You must set `acl: public-read` and set `public-base-url` to whatever fronts the bucket.

**Expiry.** A presigned URL must outlive the platform's fetch. An hour is plenty. A minute is not.

**Cleanup.** Set `delete-after: true` to remove the object once the run ends. This happens on its own context, so it works even after Ctrl-C. Turn it off if the platform might re-fetch later.

## `server`: from this machine

crier starts an HTTP server. It serves the file and stops it afterwards.

```yaml
stage:
  mode: server
  server:
    listen: 127.0.0.1:8080
    public-url: https://media.example.com     # what the platform will fetch
```

The listener serves **only** the files that were staged. It does this by random path element. Serving a directory would let a crafted path walk out of it. This server is exposed to the internet.

The `public-url` is required. crier cannot know what is in front of it. On a machine with no public address, a [tunnel](./tunnels.md) provides one.

## `url`: already hosted

```yaml
stage:
  mode: url
  url: https://cdn.example.com/card.jpg
```

This is an escape hatch for setups crier knows nothing about: a CDN, a static site, or a bucket something else uploads to. crier does not check that the URL points at the file. Only the operator knows that. Choosing this mode means you confirm the URL is correct.

## `none`

This stages nothing. It works for platforms that accept bytes. Using it with platforms that do not accept bytes is a configuration error.

Configuration keys: [`stage.*`](../configuration/stage/README.md), [`stage.s3.*`](../configuration/stage/s3.md), and [`stage.server.*`](../configuration/stage/server.md).
