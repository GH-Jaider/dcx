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

- Google Chrome (o Chromium; se autodetecta, o `--chrome /ruta`)
- `ffmpeg` en el PATH (`brew install ffmpeg`)

## TUI

- `↑↓` mover · `espacio` marcar · `a` todos/ninguno
- `f` fps (24/25/30/60) · `s` escala (1x/2x/3x) · `p` preset · `w` workers
- `enter` exportar · `q` salir/cancelar

## Presets

| preset        | codec                | uso                                    |
| ------------- | -------------------- | -------------------------------------- |
| `prores4444`  | ProRes 4444 12-bit   | masterización / edición (default)      |
| `prores422hq` | ProRes 422 HQ 10-bit | edición, la mitad de peso              |
| `h264`        | H.264 CRF 17         | publicación directa                    |

Con `--scale 2` (default) un lienzo de 1080×1080 sale a 2160×2160.

## Flags

```
--fps 30            frames por segundo
--scale 2           factor de resolución
--preset prores4444 prores4444 | prores422hq | h264
--workers 3         exports en paralelo
--out-dir DIR       salida (default: junto a cada proyecto)
--max-seconds N     corta el export (para pruebas)
--chrome RUTA       binario de Chrome
--plain             sin TUI (también se activa si no hay terminal)
```

## Desarrollo

```
make build   # compila a bin/dcx
make test    # go test ./...
make check   # fmt + vet + test
```
