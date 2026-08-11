# dcx

TUI para exportar composiciones `.dc.html` a video — varios proyectos en
paralelo, frame a frame y determinístico.

![Go](https://img.shields.io/badge/Go-1.26-3DD98A) ![ProRes](https://img.shields.io/badge/default-ProRes%204444-3DD98A)

```bash
dcx                        # busca .dc.html en el directorio actual
dcx carpeta/ otro.dc.html  # mezcla carpetas y archivos
dcx --plain --preset h264 proyectos/   # sin TUI (scripts/CI)
```

## Instalar

```bash
git clone https://github.com/GH-Jaider/dcx && cd dcx
make install   # go install ./cmd/dcx
```

Necesita `ffmpeg` en el PATH. El motor de render se lo consigue solo:
usa tu Chrome/Chromium si existe, o descarga `chrome-headless-shell`
(~95 MB, una sola vez) con tu permiso. `--download-browser` /
`--no-download` para scripts y CI.

## Presets

| preset | salida |
| --- | --- |
| `prores4444` (default) | ProRes 4444 con alfa, masterización |
| `prores422hq` / `prores422` | ProRes 422 HQ / estándar |
| `h264` / `hevc` | `.mp4` para publicar |
| `hevc-alpha` / `webm-alpha` | transparencia (Safari / resto de browsers) |
| `png-seq` | secuencia PNG bit-exacta |

En macOS con Apple Silicon codifica por hardware (VideoToolbox, ~3× más
rápido); en cualquier otra máquina cae a software solo.

## TUI

`↑↓` mover · `espacio` marcar · `a` todos · `f` fps · `s` escala ·
`p` preset · `w` workers · `enter` exportar · `q` salir

## Flags

```
--fps 30  --scale 2  --preset prores4444  --workers 3
--out-dir DIR  --max-seconds N  --browser RUTA  --plain
```

## Cómo funciona

Cada proyecto se sirve por HTTP, se abre en un tab de Chrome headless
(chromedp) y se renderiza con el protocolo determinístico del runtime dc:
un evento de seek síncrono deja el DOM en el frame exacto, se captura el
PNG y se canaliza directo a ffmpeg. Sin frames en disco, sin Node, sin
Playwright — Go puro y dos binarios (navegador + ffmpeg).
