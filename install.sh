#!/bin/sh
set -eu

REPOSITORY="anubisreal/acebridge"
SERVICE="acebridge.service"
SERVICE_FILE="/etc/systemd/system/$SERVICE"
CONFIG_FILE="/etc/default/acebridge"
BINARY="/usr/local/bin/acebridge"

if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo "$0" "$@"
  fi
  echo "Ejecuta: su -c './install.sh'"
  exit 1
fi

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

pause() {
  printf "\nPulsa Enter para continuar..."
  read -r _answer
}

architecture() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) echo "Arquitectura no compatible: $(uname -m)" >&2; exit 1 ;;
  esac
}

download_binary() {
  ARCH=$(architecture)
  TEMP_DIR=$(mktemp -d)
  trap 'rm -rf "$TEMP_DIR"' EXIT INT TERM
  FILE="acebridge-linux-$ARCH"
  BASE_URL="https://github.com/$REPOSITORY/releases/latest/download"

  echo "Descargando la última versión de AceBridge..."
  if command -v curl >/dev/null 2>&1; then
    if ! curl -fsSL "$BASE_URL/$FILE" -o "$TEMP_DIR/$FILE" || ! curl -fsSL "$BASE_URL/checksums.txt" -o "$TEMP_DIR/checksums.txt"; then
      rm -rf "$TEMP_DIR"
      trap - EXIT INT TERM
      return 1
    fi
  elif command -v wget >/dev/null 2>&1; then
    if ! wget -q "$BASE_URL/$FILE" -O "$TEMP_DIR/$FILE" || ! wget -q "$BASE_URL/checksums.txt" -O "$TEMP_DIR/checksums.txt"; then
      rm -rf "$TEMP_DIR"
      trap - EXIT INT TERM
      return 1
    fi
  else
    echo "Necesitas curl o wget para descargar AceBridge."
    return 1
  fi

  EXPECTED=$(awk -v file="$FILE" '$2 == file {print $1}' "$TEMP_DIR/checksums.txt")
  ACTUAL=$(sha256sum "$TEMP_DIR/$FILE" | awk '{print $1}')
  if [ -z "$EXPECTED" ] || [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "La comprobación de seguridad de la descarga falló."
    return 1
  fi
  install -m 0755 "$TEMP_DIR/$FILE" "$BINARY"
  rm -rf "$TEMP_DIR"
  trap - EXIT INT TERM
}

build_local_binary() {
  if ! command -v go >/dev/null 2>&1; then
    return 1
  fi
  echo "Todavía no hay una versión publicada. Compilando con el Go instalado..."
  cd "$PROJECT_DIR"
  go build -trimpath -o "$BINARY" ./cmd/acebridge
}

install_acebridge() {
  if ! download_binary; then
    build_local_binary || {
      echo "No se pudo descargar una versión publicada y Go no está instalado."
      exit 1
    }
  fi

  install -m 0644 "$PROJECT_DIR/deploy/systemd/acebridge.service" "$SERVICE_FILE"
  mkdir -p /etc/default
  if [ ! -f "$CONFIG_FILE" ]; then
    install -m 0644 "$PROJECT_DIR/deploy/systemd/acebridge.default" "$CONFIG_FILE"
  fi
  systemctl daemon-reload
  systemctl enable "$SERVICE"
  systemctl restart "$SERVICE"

  PORT=$(sed -n 's/^ACEBRIDGE_LISTEN_ADDR=://p' "$CONFIG_FILE" | tail -n 1)
  [ -n "$PORT" ] || PORT=8080
  echo
  echo "AceBridge está instalado y funcionando."
  echo "Abre en tu navegador: http://IP_DEL_SERVIDOR:$PORT"
}

change_port() {
  if [ ! -f "$CONFIG_FILE" ]; then
    echo "Instala AceBridge antes de cambiar el puerto."
    return
  fi

  CURRENT=$(sed -n 's/^ACEBRIDGE_LISTEN_ADDR=://p' "$CONFIG_FILE" 2>/dev/null | tail -n 1)
  [ -n "$CURRENT" ] || CURRENT=8080
  printf "Puerto nuevo [%s]: " "$CURRENT"
  read -r PORT
  [ -n "$PORT" ] || PORT=$CURRENT
  case "$PORT" in
    *[!0-9]*|'') echo "El puerto debe ser un número."; return ;;
  esac
  if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    echo "El puerto debe estar entre 1 y 65535."
    return
  fi
  if grep -q '^ACEBRIDGE_LISTEN_ADDR=' "$CONFIG_FILE"; then
    sed -i "s/^ACEBRIDGE_LISTEN_ADDR=.*/ACEBRIDGE_LISTEN_ADDR=:$PORT/" "$CONFIG_FILE"
  else
    printf '\nACEBRIDGE_LISTEN_ADDR=:%s\n' "$PORT" >> "$CONFIG_FILE"
  fi
  systemctl restart "$SERVICE"
  echo "Puerto cambiado. Abre: http://IP_DEL_SERVIDOR:$PORT"
}

uninstall_acebridge() {
  systemctl disable --now "$SERVICE" 2>/dev/null || true
  rm -f "$SERVICE_FILE" "$BINARY"
  systemctl daemon-reload
  echo "AceBridge fue desinstalado. Tus datos siguen en /var/lib/acebridge."
}

menu() {
  while true; do
    printf '\033[2J\033[H'
    echo "AceBridge"
    echo "========="
    echo "1. Instalar o actualizar"
    echo "2. Cambiar puerto"
    echo "3. Ver estado"
    echo "4. Ver logs"
    echo "5. Desinstalar"
    echo "0. Salir"
    printf "\nElige una opción: "
    read -r OPTION
    case "$OPTION" in
      1) install_acebridge; pause ;;
      2) change_port; pause ;;
      3) systemctl --no-pager status "$SERVICE" || true; pause ;;
      4) journalctl -u "$SERVICE" -n 80 --no-pager; pause ;;
      5) uninstall_acebridge; pause ;;
      0) exit 0 ;;
      *) echo "Opción no válida."; pause ;;
    esac
  done
}

case "${1:-install}" in
  install|update) install_acebridge ;;
  menu) menu ;;
  port) change_port ;;
  status) systemctl --no-pager status "$SERVICE" ;;
  logs) journalctl -u "$SERVICE" -f ;;
  uninstall) uninstall_acebridge ;;
  *) echo "Uso: sudo ./install.sh [install|menu|port|status|logs|uninstall]"; exit 1 ;;
esac
