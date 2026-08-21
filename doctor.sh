#!/bin/bash
# Diagnóstico de dcx: revisa la instalación, la carpeta de proyectos y la red,
# y termina con un export de prueba. Imprime un reporte legible para mandar por
# chat cuando algo falla.
#
#   bash doctor.sh "/ruta/a/la carpeta con los .dc.html"

CARPETA="$1"
PROBLEMAS=()
ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$1"; PROBLEMAS+=("$2"); }
info() { printf '    %s\n' "$1"; }
titulo() { printf '\n\033[1m%s\033[0m\n' "$1"; }

printf '\n\033[1m═══ dcx doctor ═══\033[0m\n'
printf 'fecha: %s · equipo: %s\n' "$(date '+%Y-%m-%d %H:%M')" "$(hostname -s 2>/dev/null)"

titulo "1. El programa dcx"
DCX="$(command -v dcx 2>/dev/null)"
if [ -z "$DCX" ]; then
  bad "no encuentro el comando dcx" "Instalar dcx: cd al repo y 'make install'. Si sigue igual, añadir ~/go/bin al PATH."
else
  ok "instalado en: $DCX"
  COPIAS="$(command -v -a dcx 2>/dev/null | sort -u | wc -l | tr -d ' ')"
  if [ "$COPIAS" -gt 1 ]; then
    bad "hay $COPIAS copias de dcx en el PATH (puede estar corriendo una vieja)" "Borrar las copias sobrantes de dcx; dejar solo ~/go/bin/dcx."
    command -v -a dcx | sed 's/^/      /'
  fi
  info "compilado: $("$DCX" --version 2>/dev/null || echo 'versión desconocida — binario viejo, hay que reinstalar')"
  info "archivo:   $(ls -l "$DCX" 2>/dev/null | awk '{print $6, $7, $8}')"
  case "$("$DCX" --version 2>&1)" in
    *desconocida*|*"flag provided"*|*"not defined"*)
      bad "el binario es anterior a los arreglos" "Actualizar dcx: en el repo, 'git pull && make install'." ;;
  esac
fi

titulo "2. Herramientas que dcx necesita"
if command -v ffmpeg >/dev/null 2>&1; then ok "ffmpeg: $(command -v ffmpeg)"
else bad "falta ffmpeg" "Instalar ffmpeg: 'brew install ffmpeg'."; fi

NAVEGADOR=""
for c in "$HOME/Library/Caches/dcx" "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" "/Applications/Chromium.app/Contents/MacOS/Chromium"; do
  [ -e "$c" ] && NAVEGADOR="$c" && break
done
if [ -n "$NAVEGADOR" ]; then ok "navegador para renderizar: $NAVEGADOR"
else info "sin navegador aún — dcx ofrecerá descargarlo (~95 MB) la primera vez"; fi

titulo "3. Internet (el runtime carga React desde un CDN)"
if curl -sS -m 15 -o /dev/null -w '%{http_code}' https://unpkg.com/react@18.3.1/package.json 2>/dev/null | grep -q '^[23]'; then
  ok "unpkg.com responde"
else
  bad "no hay acceso a unpkg.com" "Sin ese CDN las composiciones nunca cargan: revisar internet, VPN, proxy o firewall de la empresa."
fi

titulo "4. La carpeta de proyectos"
if [ -z "$CARPETA" ]; then
  bad "no me pasaste la carpeta" "Correr: bash doctor.sh \"/ruta/a/la carpeta\""
elif [ ! -d "$CARPETA" ]; then
  bad "esa carpeta no existe: $CARPETA" "Revisar la ruta (ojo con tildes y espacios)."
else
  ok "carpeta: $CARPETA"
  N=$(find "$CARPETA" -maxdepth 1 -name '*.dc.html' | wc -l | tr -d ' ')
  if [ "$N" -eq 0 ]; then
    bad "no hay ningún archivo .dc.html adentro" "Apuntar dcx a la carpeta que contiene los .dc.html."
  else
    ok "$N proyectos .dc.html"
    # Los .dc.html no funcionan solos: necesitan sus archivos hermanos.
    FALTA=""
    for req in support.js; do
      if grep -lq "$req" "$CARPETA"/*.dc.html 2>/dev/null && [ ! -f "$CARPETA/$req" ]; then FALTA="$FALTA $req"; fi
    done
    for d in _ds assets uploads; do
      if grep -lq "$d/" "$CARPETA"/*.dc.html 2>/dev/null && [ ! -d "$CARPETA/$d" ]; then FALTA="$FALTA $d/"; fi
    done
    if [ -n "$FALTA" ]; then
      bad "faltan archivos que los proyectos necesitan:$FALTA" "La carpeta está incompleta: descomprimir de nuevo el .zip del export y usar ESA carpeta entera (no copiar solo los .dc.html)."
    else
      ok "están los archivos de apoyo (support.js, _ds/, assets/, uploads/)"
    fi
  fi
fi

titulo "5. Export de prueba (1 segundo, el primer proyecto)"
if [ -n "$DCX" ] && [ -d "$CARPETA" ]; then
  PRIMERO="$(find "$CARPETA" -maxdepth 1 -name '*.dc.html' | sort | head -1)"
  if [ -n "$PRIMERO" ]; then
    SALIDA="$(mktemp -d)"
    info "probando: $(basename "$PRIMERO")"
    RES="$("$DCX" -plain -preset h264 -max-seconds 1 -workers 1 -no-download -out-dir "$SALIDA" "$PRIMERO" 2>&1)"
    if printf '%s' "$RES" | grep -q "listo →"; then
      ok "el export de prueba funcionó — dcx está sano en este equipo"
    else
      bad "el export de prueba falló; esto dijo:" "Mandar este reporte completo por chat."
      printf '%s\n' "$RES" | tail -12 | sed 's/^/      /'
    fi
    rm -rf "$SALIDA"
  fi
else
  info "me salto la prueba (falta dcx o la carpeta)"
fi

titulo "RESULTADO"
if [ ${#PROBLEMAS[@]} -eq 0 ]; then
  printf '  \033[32mTodo en orden.\033[0m Si un export puntual falla, mandar el mensaje de error tal cual.\n\n'
else
  printf '  Encontré %s cosa(s) que arreglar, en orden:\n\n' "${#PROBLEMAS[@]}"
  i=1
  for p in "${PROBLEMAS[@]}"; do printf '  %s. %s\n' "$i" "$p"; i=$((i+1)); done
  printf '\n'
fi
