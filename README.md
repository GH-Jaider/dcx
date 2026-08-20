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
make install   # go install ./cmd/dcx → ~/go/bin/dcx
```

`go install` deja el binario en `~/go/bin` (o `$GOBIN`). Si ese directorio
no está en tu PATH, añádelo una vez:

```bash
# zsh (macOS)
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

Alternativa sin instalar: `make build` y usar `./bin/dcx` directo.

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
(chromedp) y se renderiza frame a frame con uno de dos protocolos:

- **Runtime om** — la composición expone el svg exportable del runtime dc:
  un evento de seek síncrono deja el DOM en el frame exacto. La duración la
  declara la propia composición.
- **Composiciones CSS** — proyectos animados con `@keyframes` puros (los
  `.dc.html` de Claude Design, por ejemplo): dcx congela todas las
  animaciones con la Web Animations API y las posa en el tiempo exacto de
  cada frame.

Cada PNG se canaliza directo a ffmpeg. Sin frames en disco, sin Node, sin
Playwright — Go puro y dos binarios (navegador + ffmpeg).

## Duración (composiciones CSS)

Prioridad de mayor a menor:

1. **Declarada en el archivo** — atributos en cualquier elemento (segundos):
   `data-export-secs="12"` fija el largo, o `data-export-in="1"`
   `data-export-out="9"` exportan solo esa ventana.
2. **Inferida de los keyframes** — el ciclo más largo entre las animaciones
   `infinite`, o el final de la más tardía de las finitas. Ojo con los
   loops ambiente lentos (una órbita de 90s manda sobre todo lo demás).

`--max-seconds N` recorta cualquiera de las dos por encima.
