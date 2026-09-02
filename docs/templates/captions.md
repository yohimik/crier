# Captions

The post text is a configuration template.

```yaml
publish:
  caption: "{{ .title }} {{ .version }} is out — read more on {{ .Platform }}"
```

It runs with the same data document used to render the layout. It also includes `{{ .Platform }}`. This is the platform's own name.

## Per platform

A platform-specific caption overrides the shared one:

```yaml
publish:
  caption: "New in {{ .version }}"
  discord:
    caption: "New in {{ .version }} <@&123456789>"   # a role mention, discord only
  reddit:
    title: "{{ .product }} {{ .version }}"           # reddit requires a title
  mastodon:
    alt-text: "A release card for {{ .product }} {{ .version }}"
```

Every text-carrying key is a template. This includes `caption` everywhere, plus `tiktok.title`, `reddit.title` and `mastodon.alt-text`.

These are standard configuration keys. You can also set them with an environment variable or a flag:

```sh
CRIER_PUBLISH_DISCORD_CAPTION='shipped {{ .version }}' crier
```

## Rules

- A **plain string with no `{{`** is passed through untouched. An ordinary caption costs no parse and cannot fail.
- A **missing key is an error**. It does not output `<no value>` in a public post. The error names the configuration key that failed:

  ```
  crier: publish.telegram.caption: executing caption template: map has no entry for key "verison"
  ```

  That is exit code 3.
- The [random helpers](./pools.md) are available. They draw from the same seeded source as the layout.
- Captions are resolved **once per platform, before anything is sent**. A broken caption costs no uploads.

## Seeing them before they are sent

```sh
crier --dry-run
```

This command prints the resolved caption for each platform. It makes no network calls.

For configuration keys, check [`publish.caption`](../configuration/reference/publish.md) and each platform's own page.
