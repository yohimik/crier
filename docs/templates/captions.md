# Captions

The post text is configuration, and it is a template.

```yaml
publish:
  caption: "{{ .title }} {{ .version }} is out — read more on {{ .Platform }}"
```

It is executed with the same data document the layout was rendered with, plus
`{{ .Platform }}`, which is the platform's own name.

## Per platform

A platform's own caption wins over the shared one:

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

Every text-carrying key is a template: `caption` everywhere, plus
`tiktok.title`, `reddit.title` and `mastodon.alt-text`.

Because they are ordinary configuration keys, they take an environment variable
and a flag as well:

```sh
CRIER_PUBLISH_DISCORD_CAPTION='shipped {{ .version }}' crier publish
```

## Rules

- A **plain string with no `{{`** is passed through untouched, so an ordinary
  caption costs no parse and cannot fail.
- A **missing key is an error**, not `<no value>` in a public post, and the
  error names the configuration key that failed:

  ```
  crier: publish.telegram.caption: executing caption template: map has no entry for key "verison"
  ```

  That is exit code 3.
- The [random helpers](./pools.md) are available, drawing from the same seeded
  source the layout draws from.
- Captions are resolved **once per platform, before anything is sent**, so a
  broken one costs no uploads.

## Seeing them before they are sent

```sh
crier publish --dry-run
```

prints the resolved caption for each platform and makes no network calls.

Configuration keys: [`publish.caption`](../configuration/reference/publish.md)
and each platform's own page.
