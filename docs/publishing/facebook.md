# Facebook

Posts to a Page. Unlike Instagram it accepts an upload, so it works with no
staging at all.

## What you need

- A Facebook Page.
- A Meta app with `pages_manage_posts` and `pages_read_engagement`.
- A **Page** access token, not a user token.

## Setting it up

```yaml
publish:
  facebook:
    enabled: true
    page-id: "123456789012345"
    story: false      # true posts a Page story
    use-url: false    # true sends the staged URL instead of the bytes
```

```sh
export CRIER_PUBLISH_FACEBOOK_TOKEN="…"
```

## Photos, and stories

A photo post is one `POST /{page-id}/photos` with the file as `source` and the
caption as `message`.

A story is two calls: the photo is uploaded with `published=false`, and the id
that comes back is posted to `/{page-id}/photo_stories`. Only the second is
irreversible, so only the second skips retries.

## `use-url`

With `publish.facebook.use-url`, crier sends the staged URL rather than the
bytes — useful when the file is already on a CDN and re-uploading it is waste.
It makes the platform need staging, which it otherwise does not.

## Video

`POST /{page-id}/videos` with `source` (or `file_url`) and the caption as
`description`. Up to 1GB and 20 minutes.

## Check it

```sh
crier ping
```

Nothing is posted: ping reads the Page with `GET /{page-id}?fields=id,name`. See [the command line](../operations/cli.md#crier-ping).

Configuration keys: [`publish.facebook.*`](../configuration/reference/publish-facebook.md).
