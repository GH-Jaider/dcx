# dcx

TUI para exportar proyectos `.dc.html` (composiciones del runtime dc) a video
en máxima calidad — **Apple ProRes 4444** por defecto — con varios exports en
paralelo.

```
dcx                        # busca .dc.html en el directorio actual
dcx carpeta/ otro.dc.html  # mezcla carpetas y archivos
dcx --plain --preset h264 --scale 1 proyectos/   # sin TUI (scripts/CI)
```

## Cómo funciona

Cada job levanta un servidor HTTP efímero del directorio del proyecto, abre un
tab en un Chrome headless compartido (chromedp) y usa el protocolo
determinístico del runtime dc: dispara el evento `data-om-seek-to-time-frame`
con `sync:true` sobre el `svg[data-om-exportable-video-with-duration-secs]`,
que deja el DOM en el frame exacto de forma síncrona. Cada frame se captura
como PNG y se canaliza directo al `stdin` de un `ffmpeg` propio del job — sin
frames intermedios en disco. Los jobs corren en paralelo (`workers`), cada uno
en su tab (proceso renderer independiente de Chrome).

## Requisitos

- `ffmpeg` en el PATH (`brew install ffmpeg`)
- Motor de render: dcx se lo consigue solo. Orden de resolución: `--browser`
  / env `DCX_BROWSER` → `chrome-headless-shell` cacheado (versión pineada de
  [Chrome for Testing](https://googlechromelabs.github.io/chrome-for-testing/),
  ~95 MB descargados una sola vez a la caché de usuario) → Chrome / Chromium /
  Edge / Brave del sistema → ofrece la descarga. `--download-browser` fuerza
  el shell pineado (píxeles reproducibles entre máquinas); `--no-download`
  lo prohíbe (CI hermético).

## Encoders

Si el ffmpeg local trae VideoToolbox (macOS), los presets usan el encoder por
hardware — medido ~3× más rápido con ~1/8 de CPU y calidad de masterización
equivalente — y caen a software (`prores_ks`/`libx264`) en cualquier otra
máquina. La TUI y `--plain` indican cuál quedó activo.

## TUI

- `↑↓` mover · `espacio` marcar · `a` todos/ninguno
- `f` fps (24/25/30/60) · `s` escala (1x/2x/3x) · `p` preset · `w` workers
- `enter` exportar · `q` salir/cancelar

## Presets

| preset        | codec / salida            | uso                                            |
| ------------- | ------------------------- | ---------------------------------------------- |
| `prores4444`  | ProRes 4444 (alfa)        | masterización / edición (default)              |
| `prores422hq` | ProRes 422 HQ             | edición, la mitad de peso                      |
| `prores422`   | ProRes 422 estándar       | edición, aún más liviano                       |
| `h264`        | H.264 `.mp4`              | publicación universal                          |
| `hevc`        | H.265 `.mp4` (tag hvc1)   | mejor tamaño/calidad moderna                   |
| `hevc-alpha`  | H.265 `.mov` con alfa     | transparencia en Safari/iOS (solo VideoToolbox)|
| `webm-alpha`  | VP9 `.webm` con alfa      | transparencia en Chrome/Firefox/Edge           |
| `png-seq`     | directorio de PNGs        | compositing en NLE/AE (stream copy, bit-exacto)|

Con `--scale 2` (default) un lienzo de 1080×1080 sale a 2160×2160. Para
transparencia real la composición no debe pintar su propio fondo. Nota de
`hevc-alpha`: ffprobe reporta `yuv420p` aunque el alfa está (viaja en capa
auxiliar HEVC); Safari lo reproduce, Chrome/Firefox no — para web sirve
`hevc-alpha` + `webm-alpha` juntos.

## Flags

```
--fps 30            frames por segundo
--scale 2           factor de resolución
--preset prores4444 prores4444 | prores422hq | h264
--workers 3         exports en paralelo
--out-dir DIR       salida (default: junto a cada proyecto)
--max-seconds N     corta el export (para pruebas)
--browser RUTA      binario del navegador (también env DCX_BROWSER)
--download-browser  descarga (si hace falta) y usa el headless-shell pineado
--no-download       nunca descargar el navegador
--plain             sin TUI (también se activa si no hay terminal)
```

## Desarrollo

```
make build   # compila a bin/dcx
make test    # go test ./...
make check   # fmt + vet + test
```
