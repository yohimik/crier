# VK

```yaml
publish:
  vk:
    enabled: true
    owner-id: -123456      # negative is a community
```

```sh
export CRIER_PUBLISH_VK_TOKEN=vk1.a.…
crier ping     # is the token right?
crier
```

VK takes the bytes. It does not need [staging](../staging/README.md).

## The sign of the owner id picks the wall

`publish.vk.owner-id` is the only key that says where the post lands, and its sign carries the whole meaning.

| Value | Where the post goes |
| ----- | ------------------- |
| `-123456` | the wall of community `123456`, signed by the community |
| `123456` | the wall of user `123456` |

A negative id also supplies the `group_id` the upload calls need, so there is no second key for it. Without that id, VK hands out an upload slot on the caller's own wall and the photo cannot be attached to a community post.

There is no default. Zero is not a wall, and a zero that fell back to the token's own account would put a community post on somebody's personal page, so crier refuses to start instead.

## Getting a token

A community token is the usual choice, because it posts as the community rather than as a person.

1. Open your community, then **Manage → Settings → API usage → Access tokens**.
2. Create a token. Give it the **wall**, **photos**, **video** and **docs** scopes. Anything missing shows up later as `error 15, Access denied` on the step that needed it.
3. Copy the token into `CRIER_PUBLISH_VK_TOKEN`.

Set `publish.vk.owner-id` to the community id with a minus in front. The id is the number in `vk.com/club123456`, or the one **Manage → Settings** shows for a community with a vanity URL.

A user token works the same way for a personal wall. Create a standalone application at [vk.com/apps?act=manage](https://vk.com/apps?act=manage) and run the implicit flow with the same four scopes.

## The API version

Every call carries a `v` parameter, and VK versions the whole API rather than the URL. `publish.vk.api-version` is that value, and it defaults to `5.199`. Pin an older one there if a method's answer changes shape under you.

## How a post is made

Media never goes to the API host. Each kind asks for an upload server, POSTs the bytes there, and hands what that server said to a save method, which turns it into the attachment string a post refers to.

A **photo** takes three calls:

1. `photos.getWallUploadServer` answers with an `upload_url`.
2. POST the file to that URL as the `photo` field. The answer is a `server`, a `photo` and a `hash`. The three belong together.
3. `photos.saveWallPhoto` takes all three back and answers with an owner and an id, which become `photo<owner>_<id>`.

A **video** takes two. `video.save` hands out the upload URL and the ids at once, so the attachment string is known before the bytes go anywhere. The file goes up as the `video_file` field.

An **animated GIF** goes through the document methods: `docs.getWallUploadServer`, the file as `file`, then `docs.save`. This is not a detail. `photos.saveWallPhoto` flattens an animation into a still, and a document is the only shape that keeps it moving on a wall.

`wall.post` then takes the lot: the `owner_id`, `from_group=1` for a community, the caption as `message`, and the attachment strings joined with commas. That call is the post itself, so crier does not retry it. A 5xx from a gateway may mean the post was made and the answer was lost.

## An error inside a 200

VK answers HTTP 200 whether a call worked or not. Success is a `response` object and failure is an `error` object, so the status code alone says nothing. crier reads the envelope and reports the code as well as the message, because VK's messages are terse and its reference is indexed by code.

```
crier: vk: vk refused wall.post: error 15, Access denied
```

| Code | What it usually means |
| ---- | --------------------- |
| 5 | the token is wrong, expired or revoked |
| 15 | the token is missing the scope that call needed, or has no rights to the wall |
| 214 | posting to that wall is not allowed for this token |

These are not retried. A 200 carrying an error can sit beside a post that was made, and asking again would make a second one.

The upload servers are the other place a call can fail while looking like a success. One answers with an empty `photo` field, or the literal `[]`, when it would not take the file. crier treats that as a failure rather than posting an attachment that is not there.

## Several pages

Up to ten attachments go in one post, which is what makes a paginated changelog one entry on the wall rather than ten. A longer page list becomes several posts in a row, in order. See [pagination and carousels](../rendering/pagination.md).

## Check it

```sh
crier ping
```

Nothing is posted. Which call ping makes depends on the sign of the owner id, because the two setups fail in different places. A community wall is checked with `groups.getById`, which is where a token with no rights to the group fails, and the row names the community. A personal wall is checked with `users.get` and no ids at all, which VK answers with the account the token itself belongs to. If that account is not the wall in `publish.vk.owner-id`, the row says so, because a mistyped id is the common cause.

## Music

VK has no way for crier to attach an audio file to a wall post: the audio methods want a track already in a library rather than a file on your disk. Setting `publish.vk.music-file` is a configuration error that says so. See [music](./music.md).

Configuration keys: [`publish.vk.*`](../configuration/publish/vk.md).
