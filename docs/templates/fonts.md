# Fonts

## The default: this machine's fonts

crier scans the operating system's font directories and caches the index. Only the first run does this work. A template asks for a family by name:

```css
body { font-family: "Helvetica Neue", system-ui, sans-serif }
```

This is fine for a card you look at. It is not fine for a card you compare. A CI runner has a different font set from a laptop. A substituted face is a different picture.

## Hermetic: reproducible anywhere

```yaml
render:
  hermetic-fonts: true
```

The machine's fonts are ignored entirely. Only the `Go` and `Go Mono` faces compiled into the binary are used:

```css
body { font-family: "Go", sans-serif }
code { font-family: "Go Mono", monospace }
```

Every golden image in this repository is rendered this way. This makes them comparable across machines.

## Bundling your own

A hermetic setup still lets you use your own fonts. You can ship font files with your project and load them using `@font-face`:

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

The `render.base-url` setting resolves a relative `url()`. It turns a directory into a `file://` base. It leaves an absolute URL exactly as it is. This is a path-typed key. Setting it to `.` in a configuration file points to that file's own directory. This means your template will work from any working directory.

All the gallery examples do this. You can check [`examples/fonts/`](../../examples/fonts/) to see the shared font pool. You can also look at `template.html` in each example to see its `@font-face` block.

## A directory of fonts

The other method scans a folder. It makes the font families available by name:

```yaml
render:
  hermetic-fonts: true
  fonts-dir: [../fonts/poppins]
```

Both methods work in hermetic mode. An unreadable directory causes an error. The system does not silently substitute fonts.

## Licensing

Bundled fonts are files in your repository. Their licences travel with them. The examples use OFL faces. They keep each family's `OFL.txt` beside it.

Configuration keys:
[`render.hermetic-fonts`, `render.fonts-dir`, `render.base-url`](../configuration/render/README.md).
