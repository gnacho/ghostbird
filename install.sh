#!/bin/sh
# =============================================================================
# install.sh — Instalador one-liner de GhostBird (binario Go estático)
#
# GhostBird: drop-in replacement self-hosted de Tinybird para las estadísticas
# nativas de Ghost 6.x. Un binario, una SQLite, cero config obligatoria.
#
# Objetivo del one-liner (REQUIERE repo público; hoy el repo es privado):
#   curl -fsSL https://raw.githubusercontent.com/gnacho/ghostbird/main/install.sh | sh
#
# Mientras el repo sea privado, descarga el script y autentícalo:
#   GITHUB_TOKEN=ghp_xxx sh install.sh
#   GITHUB_TOKEN=ghp_xxx sh install.sh --repo gnacho/ghostbird
#
# Uso (descargado, recomendado para auditar antes):
#   sh install.sh                      # instala o actualiza (idempotente)
#   sh install.sh --version v1.2.3     # fija versión (tag de release)
#   sh install.sh --port 18190         # puerto explícito (aborta si está ocupado)
#   sh install.sh --from-source        # compila con go+git en vez de descargar release
#   sh install.sh --dry-run            # muestra lo que haría sin tocar el sistema
#   sh install.sh --uninstall          # desinstala (conserva datos/config salvo --purge)
#   sh install.sh --uninstall --purge  # borra también datos y config (pide confirmación)
#   sh install.sh --help
#
# Convención de release (la genera .github/workflows/release.yml):
#   - Artefactos:  ghostbird-linux-<goarch>  (binario suelto; goarch = amd64 | arm64)
#   - Checksums:   checksums.txt (formato sha256sum: "<hash>  <fichero>")
#
# Layout instalado:
#   /opt/ghostbird/bin/ghostbird           binario (0755 root:root)
#   /etc/ghostbird/ghostbird.env           config y secretos (0600; EnvironmentFile)
#   /var/lib/ghostbird/                    BD SQLite + backups (ghostbird:ghostbird 0750)
#   /etc/systemd/system/ghostbird.service  unit endurecida (sandbox, sin systemd: aviso)
#
# Códigos de salida: 1=privilegios, 2=dependencias/recursos, 3=arch/OS no soportado,
#   4=red/descarga, 5=verificación (checksum), 6=init no soportado, 64=uso incorrecto.
#
# Variable interna de pruebas (no usar en producción):
#   GHOSTBIRD_INSTALL_ROOT=<dir>  prefija TODAS las rutas del sistema y desactiva
#   elevación/usuario/chown (permite E2E con stubs de systemctl sin ser root).
# =============================================================================
set -eu

# ---------- CONFIGURACIÓN DE LA APP -------------------------------------------
APP_NAME="ghostbird"
APP_DESC="GhostBird (Tinybird drop-in for Ghost analytics)"
GH_REPO="gnacho/ghostbird"          # sobreescribible con --repo (org/repo)
DEF_PORT="18181"                    # detrás de nginx; 18080 lo ocupa GoatCounter
GITHUB_TOKEN="${GITHUB_TOKEN:-}"

# ---------- Rutas (GHOSTBIRD_INSTALL_ROOT = solo pruebas) ----------------------
PREFIX="${GHOSTBIRD_INSTALL_ROOT:-}"
TESTMODE=0
if [ -n "$PREFIX" ]; then TESTMODE=1; fi
BIN_DIR="${PREFIX}/opt/ghostbird/bin"
BIN_PATH="${BIN_DIR}/${APP_NAME}"
ENV_DIR="${PREFIX}/etc/ghostbird"
ENV_PATH="${ENV_DIR}/ghostbird.env"
DATA_DIR="${PREFIX}/var/lib/ghostbird"
UNIT_NAME="${APP_NAME}.service"
UNIT_PATH="${PREFIX}/etc/systemd/system/${UNIT_NAME}"

# ---------- Estado interno ------------------------------------------------------
REL_VERSION="latest"     # tag de release; se fija con --version o vía API
REL_TAG=""               # tag normalizado con 'v'
REL_NUM=""               # versión sin 'v' (inyección ldflags en --from-source)
BASE_URL_OVERRIDE=""     # --base-url (tests file:// o mirror)
GH_API=""
BASE_URL=""
FROM_SOURCE=0
UNINSTALL=0
PURGE=0
DRY_RUN=0
MODE="install"           # install | upgrade
SUDO=""
INIT=""                  # systemd | none
GOARCH=""
FAMILY=""
PKG=""
ADMIN_TOKEN=""
TOKEN_GENERATED=0
APP_PORT="$DEF_PORT"
PORT_EXPLICIT=0
FINAL_ADDR=""
UNATTENDED=0
TMPDIR_WORK=""
BIN_SRC=""

# =============================================================================
# Utilidades de log y error
# =============================================================================
info() { printf '  [..] %s\n' "$*"; }
ok()   { printf '  [OK] %s\n' "$*"; }
warn() { printf '  [!!] %s\n' "$*" >&2; }
fatal() {
    _code="$1"; shift
    printf '  [XX] ERROR: %s\n' "$*" >&2
    exit "$_code"
}

# Ejecuta un comando privilegiado respetando --dry-run y la elevación detectada.
run() {
    if [ "$DRY_RUN" = "1" ]; then
        printf '  [DRY-RUN] %s\n' "$*"
        return 0
    fi
    if [ -n "$SUDO" ]; then
        $SUDO "$@"
    else
        "$@"
    fi
}

# =============================================================================
# Limpieza garantizada (tempdir + trap).
# OJO: el valor de retorno de este handler se convierte en el exit code del
# script en dash (sh de Debian/Ubuntu) cuando el script acaba con un exit
# explícito (--help, --uninstall) o sin haber descargado. El `return 0` final
# es OBLIGATORIO: con la forma "[ ... ] && rm -rf" el test devolvía 1 y el
# script salía 1.
# =============================================================================
cleanup() {
    if [ -n "$TMPDIR_WORK" ] && [ -d "$TMPDIR_WORK" ]; then
        rm -rf "$TMPDIR_WORK"
    fi
    return 0
}
trap cleanup EXIT INT TERM

# =============================================================================
# Parseo de argumentos (flags largos con case, POSIX)
# =============================================================================
usage() {
    cat <<EOF
Instalador de ${APP_NAME} — ${APP_DESC}

Uso: sh $0 [opciones]

  --version <vX.Y.Z>   Fija la versión a instalar (por defecto: latest)
  --repo <org/repo>    Repo de GitHub alternativo (por defecto: ${GH_REPO})
  --port <n>           Puerto de escucha explícito (default ${DEF_PORT}; aborta si está ocupado)
  --from-source        Compila desde fuente con go+git (en vez de release)
  --uninstall          Desinstala el servicio, el binario y el usuario
  --purge              Con --uninstall: borra también datos y config (pide confirmación)
  --dry-run            Muestra las acciones sin modificar el sistema
  --help               Muestra esta ayuda

Variables de entorno:
  GITHUB_TOKEN         Token con scope repo para descargar de un repo PRIVADO
  GHOSTBIRD_INSTALL_ROOT  (solo pruebas) prefija las rutas del sistema

Sin TTY (pipe) el modo desatendido es automático; si el puerto por defecto
está ocupado se elige el siguiente libre (hasta +20) y se avisa.
EOF
    exit 0
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version)    REL_VERSION="${2:?--version requiere un valor}"; shift 2 ;;
        --version=*)  REL_VERSION="${1#*=}"; shift ;;
        --repo)       GH_REPO="${2:?--repo requiere un valor (org/repo)}"; shift 2 ;;
        --repo=*)     GH_REPO="${1#*=}"; shift ;;
        --base-url)   BASE_URL_OVERRIDE="${2:?--base-url requiere un valor}"; shift 2 ;;
        --base-url=*) BASE_URL_OVERRIDE="${1#*=}"; shift ;;
        --port)       APP_PORT="${2:?--port requiere un valor}"; PORT_EXPLICIT=1; shift 2 ;;
        --port=*)     APP_PORT="${1#*=}"; PORT_EXPLICIT=1; shift ;;
        --from-source) FROM_SOURCE=1; shift ;;
        --uninstall)  UNINSTALL=1; shift ;;
        --purge)      PURGE=1; shift ;;
        --dry-run)    DRY_RUN=1; shift ;;
        --help|-h)    usage ;;
        *) fatal 64 "opción desconocida: $1 (usa --help)" ;;
    esac
done

if [ "$PURGE" = "1" ] && [ "$UNINSTALL" = "0" ]; then
    fatal 64 "--purge solo tiene sentido junto a --uninstall"
fi
case "$GH_REPO" in
    */*) ;;
    *) fatal 64 "--repo debe tener forma org/repo (recibido: ${GH_REPO})" ;;
esac
case "$APP_PORT" in
    ''|*[!0-9]*) fatal 64 "--port debe ser numérico (recibido: ${APP_PORT})" ;;
esac
if [ "$APP_PORT" -lt 1 ] || [ "$APP_PORT" -gt 65535 ]; then
    fatal 64 "--port fuera de rango 1-65535 (recibido: ${APP_PORT})"
fi

# Copia de los argumentos originales: el parseo los consume con shift y la
# vía de re-ejecución 'su -c' los necesita completos.
SCRIPT_ARGS="$*"

# Modo desatendido si no hay terminal a la que preguntar nada.
tty_ok() { (exec 3<>/dev/tty) 2>/dev/null; }
if ! tty_ok; then UNATTENDED=1; fi

TMPDIR_WORK="$(mktemp -d)" || fatal 1 "no se pudo crear directorio temporal"

GH_API="https://api.github.com/repos/${GH_REPO}"

# =============================================================================
# Descargador abstracto (curl → wget → error). Con GITHUB_TOKEN añade la
# cabecera de autenticación (repo privado); en file:// no se envía.
# =============================================================================
fetch() {
    # fetch <url> <destino>
    _url="$1"; _dst="$2"; _hdr=""
    case "$_url" in
        file://*) ;;
        *) if [ -n "$GITHUB_TOKEN" ]; then _hdr="Authorization: Bearer ${GITHUB_TOKEN}"; fi ;;
    esac
    if command -v curl >/dev/null 2>&1; then
        if [ -n "$_hdr" ]; then
            curl -fsSL --retry 3 --connect-timeout 10 -H "$_hdr" "$_url" -o "$_dst"
        else
            curl -fsSL --retry 3 --connect-timeout 10 "$_url" -o "$_dst"
        fi
    elif command -v wget >/dev/null 2>&1; then
        if [ -n "$_hdr" ]; then
            wget -q --header="$_hdr" -O "$_dst" "$_url"
        else
            wget -q -O "$_dst" "$_url"
        fi
    else
        fatal 2 "no hay curl ni wget disponibles"
    fi
}

# =============================================================================
# Detección del entorno
# =============================================================================
detect_elevation() {
    if [ "$(id -u)" = "0" ]; then
        SUDO=""
        ok "ejecutando como root"
        return
    fi
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
        ok "elevación vía sudo"
        return
    fi
    if command -v doas >/dev/null 2>&1; then
        SUDO="doas"
        ok "elevación vía doas"
        return
    fi
    if command -v su >/dev/null 2>&1 && [ -r "$0" ] && [ -f "$0" ]; then
        info "sin sudo/doas: re-ejecutando con 'su -c' (exporta GITHUB_TOKEN si hace falta)"
        exec su -c "sh '$0' ${SCRIPT_ARGS}" root
    fi
    fatal 1 "se necesitan privilegios de root y no hay sudo, doas ni su re-ejecutable.
  Ejecuta como root:  curl -fsSL <url> -o install.sh && su -c 'sh install.sh'"
}

detect_os() {
    if [ "$(uname -s)" != "Linux" ]; then
        fatal 3 "sistema no soportado: $(uname -s) (solo Linux)"
    fi
}

detect_distro() {
    OS_ID=""; OS_LIKE=""
    if [ -r /etc/os-release ]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        OS_ID="${ID:-unknown}"
        OS_LIKE="${ID_LIKE:-}"
    elif [ -r /usr/lib/os-release ]; then
        # shellcheck disable=SC1091
        . /usr/lib/os-release
        OS_ID="${ID:-unknown}"
        OS_LIKE="${ID_LIKE:-}"
    else
        OS_ID="unknown"
    fi
    case " ${OS_ID} ${OS_LIKE} " in
        *debian*|*ubuntu*|*raspbian*|*mint*) FAMILY="debian" ;;
        *rhel*|*fedora*|*centos*|*rocky*|*almalinux*|*ol*) FAMILY="rhel" ;;
        *arch*|*manjaro*) FAMILY="arch" ;;
        *suse*) FAMILY="suse" ;;
        *alpine*) FAMILY="alpine" ;;
        *) FAMILY="unknown" ;;
    esac
    ok "distro: ${OS_ID} (familia: ${FAMILY})"
}

# Solo linux/amd64 y linux/arm64: son las únicas matrices que publica el CI.
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  GOARCH="amd64" ;;
        aarch64|arm64) GOARCH="arm64" ;;
        *) fatal 3 "arquitectura no soportada: $(uname -m).
  GhostBird publica binarios solo para linux/amd64 y linux/arm64." ;;
    esac
    ok "arquitectura: $(uname -m) → linux/${GOARCH}"
}

detect_pkg_manager() {
    if command -v apt-get >/dev/null 2>&1; then PKG="apt"
    elif command -v dnf >/dev/null 2>&1; then PKG="dnf"
    elif command -v yum >/dev/null 2>&1; then PKG="yum"
    elif command -v zypper >/dev/null 2>&1; then PKG="zypper"
    elif command -v pacman >/dev/null 2>&1; then PKG="pacman"
    elif command -v apk >/dev/null 2>&1; then PKG="apk"
    else PKG="none"
    fi
    ok "gestor de paquetes: ${PKG}"
}

pkg_install() {
    [ "$PKG" = "none" ] && fatal 2 "sin gestor de paquetes conocido: instala manualmente: $*"
    info "instalando dependencias: $*"
    case "$PKG" in
        apt)    run env DEBIAN_FRONTEND=noninteractive apt-get update -qq
                run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" ;;
        dnf)    run dnf install -y "$@" ;;
        yum)    run yum install -y "$@" ;;
        zypper) run zypper --non-interactive install -y "$@" ;;
        pacman) run pacman -Sy --noconfirm --needed "$@" ;;
        apk)    run apk add --no-cache "$@" ;;
    esac
}

# El artefacto es un binario suelto (sin tarball): solo hacen falta
# sha256sum y un descargador. --from-source exige go y git aparte.
ensure_dependencies() {
    MISSING=""
    command -v sha256sum >/dev/null 2>&1 || MISSING="$MISSING sha256sum"
    if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
        MISSING="$MISSING curl"
    fi
    if [ "$FROM_SOURCE" = "1" ]; then
        command -v go >/dev/null 2>&1 || MISSING="$MISSING go"
        command -v git >/dev/null 2>&1 || MISSING="$MISSING git"
    fi
    if [ -n "$MISSING" ]; then
        # En servidor mínimo casi siempre falta curl; go/git no los instala el
        # instalador (toolchain de build): --from-source los exige ya presentes.
        ONLY_RUNTIME=""
        for dep in $MISSING; do
            case "$dep" in go|git) ;;
                *) ONLY_RUNTIME="$ONLY_RUNTIME $dep" ;;
            esac
        done
        if [ -n "$ONLY_RUNTIME" ]; then
            # shellcheck disable=SC2086
            pkg_install ${ONLY_RUNTIME# }
        else
            fatal 2 "--from-source requiere 'go' y 'git' en PATH (no se instalan automáticamente)"
        fi
    fi
    command -v sha256sum >/dev/null 2>&1 || fatal 2 "falta sha256sum y no se pudo instalar"
    command -v install >/dev/null 2>&1 || fatal 2 "falta el comando 'install' (coreutils)"
    if command -v curl >/dev/null 2>&1; then
        ok "descargador: curl"
    else
        ok "descargador: wget"
    fi
}

# Pre-flight de conectividad antes de tocar nada. Se salta con file:// o
# --from-source (el propio clone fallará con un error accionable).
preflight() {
    if [ "$FROM_SOURCE" = "1" ]; then return 0; fi
    case "$BASE_URL_OVERRIDE" in
        file://*) return 0 ;;
    esac
    info "pre-flight: comprobando conectividad con GitHub…"
    if ! fetch "https://api.github.com" /dev/null 2>/dev/null && \
       ! fetch "https://github.com" /dev/null 2>/dev/null; then
        fatal 4 "sin conectividad con GitHub. Revisa red/DNS/proxy antes de continuar."
    fi
    ok "conectividad OK"
}

# =============================================================================
# Versión: API releases/latest o pin del usuario. Normaliza el prefijo 'v'
# del tag UNA vez (los tags son vX.Y.Z pero --version acepta ambas formas).
# =============================================================================
resolve_version() {
    if [ "$REL_VERSION" = "latest" ]; then
        case "$BASE_URL_OVERRIDE" in
            file://*) fatal 64 "--base-url file:// no tiene API: fija también --version" ;;
        esac
        info "resolviendo última versión publicada…"
        fetch "${GH_API}/releases/latest" "${TMPDIR_WORK}/latest.json" \
            || fatal 4 "no se pudo consultar ${GH_API}/releases/latest (¿repo privado? exporta GITHUB_TOKEN)"
        REL_VERSION="$(grep '"tag_name"' "${TMPDIR_WORK}/latest.json" | head -n1 \
            | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
        [ -n "$REL_VERSION" ] || fatal 4 "no se pudo resolver la última versión desde ${GH_API}"
    fi
    case "$REL_VERSION" in
        v*) REL_TAG="$REL_VERSION" ;;
        *)  REL_TAG="v${REL_VERSION}" ;;
    esac
    REL_NUM="${REL_TAG#v}"
    BASE_URL="${BASE_URL_OVERRIDE:-https://github.com/${GH_REPO}/releases/download/${REL_TAG}}"
    ok "versión: ${REL_TAG} (asset: ${APP_NAME}-linux-${GOARCH})"
}

# =============================================================================
# Recursos: disco, RAM y puerto (solo install fresca; en upgrade el puerto
# manda en el env existente y NUNCA se cambia)
# =============================================================================
port_in_use() {
    if command -v ss >/dev/null 2>&1; then
        ss -tln 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
    elif command -v netstat >/dev/null 2>&1; then
        netstat -tln 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
    else
        return 1  # sin herramienta: se asume libre
    fi
}

pick_port() {
    _p="$1"; _end=$((_p + 20))
    while [ "$_p" -le "$_end" ]; do
        if ! port_in_use "$_p"; then printf '%s' "$_p"; return 0; fi
        _p=$((_p + 1))
    done
    return 1
}

# choose_port <querido>: con TTY pregunta (sugiere el siguiente libre y
# rechaza inválidos/ocupados); sin TTY usa el siguiente libre.
choose_port() {
    _want="$1"
    _next="$(pick_port "$((_want + 1))")" || _next=""
    if [ "$UNATTENDED" -eq 0 ] && tty_ok; then
        while :; do
            printf 'El puerto %s está ocupado. ¿En qué puerto escucha %s? [%s] ' \
                "$_want" "$APP_NAME" "${_next:-ninguno libre}" > /dev/tty
            IFS= read -r _r < /dev/tty || _r=""
            _r="${_r:-$_next}"
            case "$_r" in
                ''|*[!0-9]*) printf 'Introduce un número de puerto.\n' > /dev/tty; continue ;;
            esac
            if [ "$_r" -lt 1 ] || [ "$_r" -gt 65535 ]; then
                printf 'Fuera de rango (1-65535).\n' > /dev/tty; continue
            fi
            if port_in_use "$_r"; then
                printf 'El puerto %s también está ocupado.\n' "$_r" > /dev/tty; continue
            fi
            printf '%s' "$_r"; return 0
        done
    fi
    [ -n "$_next" ] || return 1
    printf '%s' "$_next"
}

check_resources() {
    AVAIL_MB="$(df -Pm "${PREFIX:-/}" 2>/dev/null | awk 'NR==2 {print $4}')"
    if [ -n "$AVAIL_MB" ]; then
        if [ "$AVAIL_MB" -lt 150 ]; then
            fatal 2 "disco insuficiente: ${AVAIL_MB} MB libres (mínimo 150 MB)"
        fi
        if [ "$AVAIL_MB" -lt 300 ]; then
            warn "poco disco: ${AVAIL_MB} MB libres (recomendado 300+ MB)"
        fi
        ok "disco: ${AVAIL_MB} MB libres"
    fi
    MEM_MB="$(awk '/^MemAvailable:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null)"
    if [ -n "$MEM_MB" ] && [ "$MEM_MB" -lt 128 ]; then
        warn "poca RAM: ${MEM_MB} MB disponibles (recomendado 128+ MB)"
    fi
    if [ "$PORT_EXPLICIT" = "1" ] && [ "$MODE" = "upgrade" ]; then
        warn "--port ignorado en upgrade: el puerto manda en ${ENV_PATH}"
    fi
    if [ "$MODE" = "install" ]; then
        if port_in_use "$APP_PORT"; then
            if [ "$PORT_EXPLICIT" = "1" ]; then
                fatal 2 "el puerto ${APP_PORT} (pedido con --port) está ocupado.
  Revisa quién lo usa:  ss -tlnp | grep :${APP_PORT}"
            fi
            APP_PORT="$(choose_port "$APP_PORT")" \
                || fatal 2 "puerto ${APP_PORT} ocupado y ninguno libre en los 20 siguientes"
            warn "puerto por defecto ocupado: GhostBird escuchará en ${APP_PORT}"
        else
            ok "puerto ${APP_PORT} libre"
        fi
    fi
}

# =============================================================================
# Descarga del binario del release + verificación sha256 obligatoria.
# En --dry-run TAMBIÉN se descarga y verifica (contrato del dry-run veraz:
# lo que anuncia = lo que hará). El artefacto es un binario suelto, no un
# tarball: no hay tar -tzf, pero sí [ -s ] tras la verificación.
# =============================================================================
download_and_verify() {
    ASSET="${APP_NAME}-linux-${GOARCH}"
    info "descargando ${ASSET} (${REL_TAG})…"
    fetch "${BASE_URL}/${ASSET}" "${TMPDIR_WORK}/${ASSET}" \
        || fatal 4 "falló la descarga de ${BASE_URL}/${ASSET}"
    fetch "${BASE_URL}/checksums.txt" "${TMPDIR_WORK}/checksums.txt" \
        || fatal 4 "falló la descarga de ${BASE_URL}/checksums.txt"

    EXPECTED="$(grep " ${ASSET}\$" "${TMPDIR_WORK}/checksums.txt" | awk '{print $1}')"
    [ -n "$EXPECTED" ] || fatal 5 "${ASSET} no aparece en checksums.txt (¿convención de nombres distinta?)"
    ACTUAL="$(sha256sum "${TMPDIR_WORK}/${ASSET}" | awk '{print $1}')"
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        fatal 5 "checksum NO coincide para ${ASSET}:
  esperado: ${EXPECTED}
  obtenido: ${ACTUAL}
  Instalación abortada por seguridad."
    fi
    ok "checksum sha256 verificado"
    [ -s "${TMPDIR_WORK}/${ASSET}" ] || fatal 5 "el binario descargado está vacío"
    chmod 0755 "${TMPDIR_WORK}/${ASSET}"
    BIN_SRC="${TMPDIR_WORK}/${ASSET}"
}

# =============================================================================
# --from-source: clona el tag y compila el binario estático aquí mismo.
# Requiere go y git (no los instala el instalador).
# =============================================================================
build_from_source() {
    info "clonando ${GH_REPO} (${REL_TAG})…"
    if [ -n "$GITHUB_TOKEN" ]; then
        ( git -c http.extraHeader="Authorization: Bearer ${GITHUB_TOKEN}" \
              clone --depth 1 --branch "$REL_TAG" \
              "https://github.com/${GH_REPO}.git" "${TMPDIR_WORK}/src" ) \
            || fatal 4 "falló el clone de https://github.com/${GH_REPO}.git (tag ${REL_TAG})"
    else
        ( git clone --depth 1 --branch "$REL_TAG" \
              "https://github.com/${GH_REPO}.git" "${TMPDIR_WORK}/src" ) \
            || fatal 4 "falló el clone de https://github.com/${GH_REPO}.git (tag ${REL_TAG})"
    fi
    info "compilando (CGO_ENABLED=0, versión ${REL_NUM})…"
    ( cd "${TMPDIR_WORK}/src" && \
      CGO_ENABLED=0 go build -trimpath \
          -ldflags "-s -w -X main.version=${REL_NUM}" \
          -o "${TMPDIR_WORK}/${APP_NAME}" ./cmd/ghostbird ) \
        || fatal 2 "la compilación falló (revisa la salida de go build)"
    [ -s "${TMPDIR_WORK}/${APP_NAME}" ] || fatal 5 "la compilación no produjo binario"
    chmod 0755 "${TMPDIR_WORK}/${APP_NAME}"
    BIN_SRC="${TMPDIR_WORK}/${APP_NAME}"
    ok "binario compilado desde fuente"
}

# =============================================================================
# Idempotencia: instalación previa → upgrade
# =============================================================================
detect_previous_install() {
    if [ -x "$BIN_PATH" ] || [ -f "$UNIT_PATH" ]; then
        MODE="upgrade"
        ok "instalación previa detectada → modo actualización"
    else
        MODE="install"
        ok "instalación limpia"
    fi
}

create_system_user() {
    if [ "$TESTMODE" = "1" ]; then return 0; fi
    if id "$APP_NAME" >/dev/null 2>&1; then
        ok "usuario de sistema '${APP_NAME}' ya existe"
        return
    fi
    info "creando usuario de sistema '${APP_NAME}'…"
    if command -v useradd >/dev/null 2>&1; then
        run useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_NAME" \
            || run useradd --system --no-create-home --shell /sbin/nologin "$APP_NAME"
    elif command -v adduser >/dev/null 2>&1; then
        # BusyBox (Alpine)
        run adduser -S -D -H -s /sbin/nologin "$APP_NAME"
    else
        fatal 2 "no hay useradd ni adduser para crear el usuario de sistema"
    fi
}

gen_token() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

# Directorio de datos (BD + backups), propiedad del usuario de servicio.
secure_dirs() {
    run mkdir -p "$DATA_DIR" "${DATA_DIR}/backups"
    if [ "$TESTMODE" = "0" ]; then
        run chown "$APP_NAME:$APP_NAME" "$DATA_DIR" "${DATA_DIR}/backups"
        run chmod 0750 "$DATA_DIR"
    fi
}

install_binary() {
    run mkdir -p "$BIN_DIR"
    if [ "$MODE" = "upgrade" ] && [ -f "$BIN_PATH" ]; then
        run cp -a "$BIN_PATH" "${BIN_PATH}.bak"
        info "backup del binario anterior: ${BIN_PATH}.bak"
    fi
    if [ "$TESTMODE" = "0" ]; then
        run install -T -m 0755 -o root -g root "$BIN_SRC" "$BIN_PATH"
    else
        run install -T -m 0755 "$BIN_SRC" "$BIN_PATH"
    fi
    if [ "$DRY_RUN" = "1" ]; then
        info "[DRY-RUN] el binario quedaría en ${BIN_PATH}"
    else
        ok "binario instalado: ${BIN_PATH}"
    fi

    # SELinux: etiquetar como binario, con degradación a warning.
    if [ "$TESTMODE" = "0" ] && command -v getenforce >/dev/null 2>&1 \
        && [ "$(getenforce)" = "Enforcing" ]; then
        if command -v chcon >/dev/null 2>&1; then
            run chcon -t bin_t "$BIN_PATH" && ok "SELinux: contexto bin_t aplicado"
        else
            warn "SELinux Enforcing sin chcon: el servicio podría no arrancar"
        fi
    fi
}

# Fichero de entorno: en upgrade NUNCA se sobreescribe (solo se COMPLETA con
# el token si faltaba). El token generado se muestra UNA sola vez.
write_env() {
    if [ -f "$ENV_PATH" ]; then
        if grep -q '^GHOSTBIRD_ADMIN_TOKEN=' "$ENV_PATH"; then
            ok "env conservado: ${ENV_PATH} (token existente, no se re-imprime)"
        else
            ADMIN_TOKEN="$(gen_token)"
            [ -n "$ADMIN_TOKEN" ] || fatal 2 "no se pudo generar el token aleatorio"
            if [ "$DRY_RUN" = "1" ]; then
                info "[DRY-RUN] añadiría GHOSTBIRD_ADMIN_TOKEN al env existente"
            else
                printf 'GHOSTBIRD_ADMIN_TOKEN=%s\n' "$ADMIN_TOKEN" >> "$ENV_PATH"
                warn "env existía sin GHOSTBIRD_ADMIN_TOKEN: token generado y añadido"
            fi
            TOKEN_GENERATED=1
        fi
        FINAL_ADDR="$(grep '^GHOSTBIRD_ADDR=' "$ENV_PATH" | tail -n1 | cut -d= -f2-)"
        if [ -z "$FINAL_ADDR" ]; then FINAL_ADDR="127.0.0.1:${APP_PORT}"; fi
        return
    fi
    ADMIN_TOKEN="$(gen_token)"
    [ -n "$ADMIN_TOKEN" ] || fatal 2 "no se pudo generar el token aleatorio"
    FINAL_ADDR="127.0.0.1:${APP_PORT}"
    TOKEN_GENERATED=1
    if [ "$DRY_RUN" = "1" ]; then
        info "[DRY-RUN] escribiría ${ENV_PATH} (0600) con GHOSTBIRD_* y token aleatorio"
        return
    fi
    _envtmp="${TMPDIR_WORK}/ghostbird.env"
    cat > "$_envtmp" <<EOF
# GhostBird — config del servicio. Edita y reinicia: systemctl restart ${APP_NAME}
# (mando: el env se aplica cuando ExecStart no pasa el flag equivalente)
GHOSTBIRD_ADDR=${FINAL_ADDR}
GHOSTBIRD_DB=${DATA_DIR}/ghostbird.db
GHOSTBIRD_ADMIN_TOKEN=${ADMIN_TOKEN}
GHOSTBIRD_BACKUP_DIR=${DATA_DIR}/backups

# Opcionales (descomenta para cambiar):
# GHOSTBIRD_INGEST_TOKEN=secreto        # auth de /v0/events (vacío = sin auth)
# GHOSTBIRD_STATS_TOKEN=                # token estático alternativo de pipes
# GHOSTBIRD_RETENTION_DAYS=0            # días de eventos raw (0 = ilimitado)
# GHOSTBIRD_LOG_LEVEL=info              # debug|info|warn|error
# GHOSTBIRD_TRUST_PROXY=1               # primera IP de X-Forwarded-For
EOF
    run mkdir -p "$ENV_DIR"
    if [ "$TESTMODE" = "0" ]; then
        run install -T -m 0600 -o root -g root "$_envtmp" "$ENV_PATH"
    else
        run install -T -m 0600 "$_envtmp" "$ENV_PATH"
    fi
    ok "env escrito: ${ENV_PATH} (0600)"
}

# =============================================================================
# Init: solo systemd real (sd_booted). Sin systemd NO es error: se instala
# todo y se imprime cómo arrancar a mano (fallback razonable).
# =============================================================================
detect_init() {
    _pid1="$(ps -p 1 -o comm= 2>/dev/null || true)"
    if command -v systemctl >/dev/null 2>&1 && \
       { [ -d /run/systemd/system ] || [ "$_pid1" = "systemd" ]; }; then
        INIT="systemd"
    else
        INIT="none"
    fi
    ok "init: ${INIT}"
}

install_service() {
    if [ "$INIT" != "systemd" ]; then
        warn "sin systemd real: NO se instala servicio (binario y config SÍ instalados)."
        info "arranque manual:
  set -a; . ${ENV_PATH}; set +a; exec ${BIN_PATH}"
        return 0
    fi
    _unittmp="${TMPDIR_WORK}/ghostbird.service"
    cat > "$_unittmp" <<EOF
[Unit]
Description=${APP_DESC}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${APP_NAME}
Group=${APP_NAME}
EnvironmentFile=-${ENV_PATH}
ExecStart=${BIN_PATH}
Restart=always
RestartSec=10

# --- Sandbox (unit de referencia del README) ---
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}
ProtectHome=yes
PrivateTmp=true
MemoryMax=256M
StateDirectory=${APP_NAME}
UMask=007

[Install]
WantedBy=multi-user.target
EOF
    info "escribiendo unit ${UNIT_PATH}…"
    if [ "$DRY_RUN" = "1" ]; then
        printf '  [DRY-RUN] install -m 0644 → %s\n' "$UNIT_PATH"
    else
        run mkdir -p "$(dirname "$UNIT_PATH")"
        run install -T -m 0644 "$_unittmp" "$UNIT_PATH"
    fi
    run systemctl daemon-reload
    run systemctl enable --now "$UNIT_NAME"
    if [ "$MODE" = "upgrade" ]; then
        run systemctl restart "$UNIT_NAME"
    fi
}

# =============================================================================
# Verificación post-arranque: is-active con reintentos + healthz (aviso).
# Si no arranca: logs en pantalla y exit 7.
# =============================================================================
verify_running() {
    if [ "$DRY_RUN" = "1" ] || [ "$INIT" != "systemd" ]; then return 0; fi
    info "verificando que el servicio arranca…"
    i=0
    while [ "$i" -lt 15 ]; do
        if systemctl is-active --quiet "$UNIT_NAME"; then
            ok "servicio activo"
            _hp="${FINAL_ADDR##*:}"
            if command -v curl >/dev/null 2>&1; then
                if curl -fsS --max-time 3 "http://127.0.0.1:${_hp}/healthz" >/dev/null 2>&1; then
                    ok "healthz responde en 127.0.0.1:${_hp}"
                else
                    warn "healthz no responde todavía en 127.0.0.1:${_hp} (revisa en unos segundos)"
                fi
            fi
            return 0
        fi
        i=$((i + 1))
        sleep 1
    done
    warn "el servicio NO arrancó. Últimas líneas de log:"
    journalctl -u "$UNIT_NAME" -n 30 --no-pager >&2 || true
    fatal 7 "verificación post-arranque fallida (revisa los logs de arriba)"
}

# =============================================================================
# --uninstall: deshace todo; datos/config se conservan salvo --purge
# (purge pide confirmación por /dev/tty; sin TTY se niega).
# =============================================================================
confirm_purge() {
    if tty_ok; then
        printf 'Esto BORRARÁ definitivamente:\n  %s\n  %s (BD + backups)\nEscribe "yes" para confirmar: ' \
            "$ENV_PATH" "$DATA_DIR" > /dev/tty
        IFS= read -r _ans < /dev/tty || _ans=""
        case "$_ans" in
            yes|YES|Yes|si|sí|S|s) return 0 ;;
            *) return 1 ;;
        esac
    fi
    return 2
}

do_uninstall() {
    detect_init
    info "desinstalando ${APP_NAME}…"
    if [ "$INIT" = "systemd" ]; then
        run systemctl disable --now "$UNIT_NAME" 2>/dev/null || true
        run rm -f "$UNIT_PATH"
        run systemctl daemon-reload
    else
        warn "init sin systemd: si el proceso corre, páralo a mano (pkill ${APP_NAME})"
    fi
    run rm -f "$BIN_PATH" "${BIN_PATH}.bak"
    run rmdir "$BIN_DIR" 2>/dev/null || true
    run rmdir "${PREFIX}/opt/ghostbird" 2>/dev/null || true
    if [ "$TESTMODE" = "0" ] && id "$APP_NAME" >/dev/null 2>&1; then
        run userdel "$APP_NAME" 2>/dev/null || run deluser "$APP_NAME" 2>/dev/null \
            || warn "no se pudo borrar el usuario '${APP_NAME}' (bórralo a mano)"
        run groupdel "$APP_NAME" 2>/dev/null || true
    fi
    if [ "$PURGE" = "1" ]; then
        _rc=0
        confirm_purge || _rc=$?
        case "$_rc" in
            0)  run rm -rf "$DATA_DIR"
                run rm -f "$ENV_PATH"
                run rmdir "$ENV_DIR" 2>/dev/null || true
                ok "datos y config borrados (purge)"
                ;;
            1)  info "confirmación denegada: datos y config conservados" ;;
            2)  fatal 1 "--purge necesita confirmación interactiva y no hay /dev/tty.
  Ejecútalo desde una terminal real." ;;
        esac
    else
        if [ -d "$DATA_DIR" ] || [ -f "$ENV_PATH" ]; then
            info "datos conservados: ${DATA_DIR} y ${ENV_PATH} (usa --purge para borrarlos)"
        fi
    fi
    ok "desinstalación completada"
    exit 0
}

# =============================================================================
# Resumen final accionable
# =============================================================================
print_summary() {
    printf '\n'
    printf '  ====================================================\n'
    printf '   %s %s — %s\n' "$APP_NAME" "${REL_TAG:-?}" \
        "$([ "$MODE" = "upgrade" ] && echo "actualizado" || echo "instalado")"
    printf '  ====================================================\n'
    printf '   Binario:    %s\n' "$BIN_PATH"
    printf '   Datos:      %s (BD SQLite + backups diarios, rotación 14 días)\n' "$DATA_DIR"
    printf '   Config:     %s (0600, EnvironmentFile)\n' "$ENV_PATH"
    if [ "$INIT" = "systemd" ]; then
        printf '   Servicio:   %s (systemd)\n' "$UNIT_NAME"
        printf '   Escucha:    %s (detrás de tu nginx; ver README sección Deploy)\n' "$FINAL_ADDR"
    else
        printf '   Servicio:   manual (sin systemd): set -a; . %s; set +a; %s\n' "$ENV_PATH" "$BIN_PATH"
    fi
    if [ "$TOKEN_GENERATED" = "1" ] && [ "$DRY_RUN" = "1" ]; then
        printf '\n   Admin token: se generará al instalar de verdad (y se mostrará entonces)\n'
    elif [ "$TOKEN_GENERATED" = "1" ]; then
        printf '\n   GHOSTBIRD_ADMIN_TOKEN (GUÁRDALO, no se volverá a mostrar):\n     %s\n' "$ADMIN_TOKEN"
        printf '\n   Config de Ghost (config.production.json, mismo secreto):\n'
        printf '     "tinybird": { "workspaceId": "ghostbird-local",\n'
        printf '                   "adminToken": "%s", ... }\n' "$ADMIN_TOKEN"
    else
        printf '\n   Admin token: existente en %s (no se re-imprime)\n' "$ENV_PATH"
    fi
    printf '\n   Comandos útiles:\n'
    if [ "$INIT" = "systemd" ]; then
        printf '     systemctl status %s\n' "$UNIT_NAME"
        printf '     journalctl -u %s -f\n' "$UNIT_NAME"
    fi
    printf '   Actualizar:    re-ejecuta este instalador (idempotente)\n'
    printf '   Desinstalar:   sh %s --uninstall [--purge]\n' "$0"
    printf '\n'
}

# =============================================================================
# MAIN
# =============================================================================
main() {
    printf '\n== Instalador de %s ==\n\n' "$APP_NAME"
    if [ "$DRY_RUN" = "1" ]; then
        warn "modo --dry-run: no se modificará el sistema"
    fi
    if [ "$TESTMODE" = "0" ]; then
        detect_elevation "$@"
    fi
    if [ "$UNINSTALL" = "1" ]; then
        do_uninstall
    fi
    detect_os
    detect_arch
    detect_distro
    detect_pkg_manager
    ensure_dependencies
    preflight
    detect_previous_install
    check_resources
    resolve_version
    if [ "$FROM_SOURCE" = "1" ]; then
        if [ "$DRY_RUN" = "1" ]; then
            info "[DRY-RUN] se clonaría ${GH_REPO} (${REL_TAG}) y compilaría con go"
            BIN_SRC="${TMPDIR_WORK}/${APP_NAME}"
        else
            build_from_source
        fi
    else
        download_and_verify
    fi
    create_system_user
    install_binary
    secure_dirs
    write_env
    detect_init
    install_service
    verify_running
    print_summary
}

main "$@"
