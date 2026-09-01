# Fonts

## The default: this machine's fonts

crier scans the operating system's font directories and caches the index, so
only the first run pays for the walk. A template asks for a family by name:

```css
body { font-family: "Helvetica Neue", system-ui, sans-serif }
```

This is fine for a card you look at. It is not fine for a card you compare:
a CI runner has a different font set from a laptop, and a substituted face is a
different picture.

## Hermetic: reproducible anywhere

```yaml
render:
  hermetic-fonts: true
```

The machine's fonts are left out entirely. What remains are the `Go` and
`Go Mono` faces compiled into the binary:

```css
body { font-family: "Go", sans-serif }
code { font-family: "Go Mono", monospace }
```

Every golden image in this repository is rendered this way, which is what makes
them comparable across machines.

## Bundling your own

Hermetic does not mean "no fonts of your own". A project can ship its faces and
load them with `@font-face`:

```css
@font-face { font-family: "Poppins"; font-weight: 400; src: url("../fonts/poppins/Poppins-Regular.ttf") }
@font-face { font-family: "Poppins"; font-weight: 700; src: url("../fonts/poppins/Poppins-Bold.ttf") }

body { font-family: "Poppins", sans-serif }
```

```yaml
render:
  hermetic-fonts: true
  base-url: .        # relative src resolves against the config file's folder
```

`render.base-url` is what makes a relative `url()` resolve: a directory becomes
a `file://` base, and an absolute URL is used as it is. Because it is a
path-typed key, `.` in a configuration file means that file's own directory —
so the template works from any working directory.

The gallery examples all do this; see
[`examples/fonts/`](../../examples/fonts/) for the shared pool and each
example's `template.html` for the `@font-face` block.

## A directory of fonts

The other way in scans a folder and makes its families available by name:

```yaml
render:
  hermetic-fonts: true
  fonts-dir: [../fonts/poppins]
```

Both work in hermetic mode. A directory that cannot be read is an error rather
than a silent substitution.

## Licensing

Bundled fonts are files in your repository, and their licences travel with
them. The examples use OFL faces and keep each family's `OFL.txt` beside it.

Configuration keys:
[`render.hermetic-fonts`, `render.fonts-dir`, `render.base-url`](../configuration/reference/render.md).
