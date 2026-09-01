# Staging

Instagram and TikTok's photo flow will not take an upload. They take a **public
URL** and fetch it themselves, from their own servers. Staging is how a
rendered file gets one.

Every other platform accepts the bytes, so a project that publishes only to
those needs none of this.

```yaml
stage:
  mode: none      # none, s3, server or url
```

If a platform that needs a URL is enabled with `stage.mode: none`, crier refuses
before rendering anything:

```
crier: instagram can only be given a URL for the media, and stage.mode is none;
set stage.mode to s3, server or url
```

That is exit code 1.

## `s3` — an object store

The most reliable option. Any S3-compatible endpoint: AWS, MinIO, Cloudflare
R2, Backblaze B2, DigitalOcean Spaces.

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

An endpoint written with a scheme — `https://minio.example` — is accepted, and
the scheme decides `use-ssl`.

**Presigned or public.** `presign: true` works on a private bucket and is the
default. With `presign: false` the object needs to be publicly readable, so set
`acl: public-read` and `public-base-url` to whatever fronts the bucket.

**Expiry.** A presigned URL must outlive the platform's fetch. An hour is
plenty; a minute is not.

**Cleanup.** With `delete-after: true` the object is removed once the run ends,
on a context of its own so it happens even after Ctrl-C. Turn it off when the
platform may re-fetch later.

## `server` — from this machine

crier starts an HTTP server, serves the file, and stops it afterwards.

```yaml
stage:
  mode: server
  server:
    listen: 127.0.0.1:8080
    public-url: https://media.example.com     # what the platform will fetch
```

The listener serves **only** the files that were staged, by random path element
— serving a directory would let a crafted path walk out of it, and this server
is exposed to the internet.

`public-url` is required, because crier cannot know what is in front of it. On
a machine with no public address, a [tunnel](./tunnels.md) provides one.

## `url` — already hosted

```yaml
stage:
  mode: url
  url: https://cdn.example.com/card.jpg
```

The escape hatch for a setup crier knows nothing about: a CDN, a static site, a
bucket something else uploads to. crier does not check that the URL points at
the file — only the operator knows that, and saying so is what choosing this
mode means.

## `none`

Stage nothing. Platforms that accept bytes work; platforms that do not are a
configuration error.

Configuration keys: [`stage.*`](../configuration/reference/stage.md),
[`stage.s3.*`](../configuration/reference/stage-s3.md),
[`stage.server.*`](../configuration/reference/stage-server.md).
