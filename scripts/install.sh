#!/bin/sh
# Custodia single-command installer for Linux x86_64/arm64.
# Verifies the release manifest signature and archive checksum before touching
# the host. Idempotent: data and master.key are preserved; use --uninstall to
# remove the service and binary (data only with --purge).
set -eu

PREFIX=/usr/local
DATA_DIR=/var/lib/custodia
CONFIG_DIR=/etc/custodia
SERVICE_USER=custodia
LISTEN=0.0.0.0:8443
PUBLIC_ORIGIN=
TLS_CERT=
TLS_KEY=
PACKAGE=
PACKAGE_SHA256=
SIGNATURE=
ALLOWED_SIGNERS=
UPDATE_MANIFEST=
NO_SYSTEMD=0
UNINSTALL=0
PURGE=0
VERSION=
ARCH=amd64
case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;; esac

die() { echo "custodia installer: $*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION=$2; shift 2 ;;
    --prefix) PREFIX=$2; shift 2 ;;
    --data-dir) DATA_DIR=$2; shift 2 ;;
    --config-dir) CONFIG_DIR=$2; shift 2 ;;
    --user) SERVICE_USER=$2; shift 2 ;;
    --listen) LISTEN=$2; shift 2 ;;
    --public-origin) PUBLIC_ORIGIN=$2; shift 2 ;;
    --tls-cert) TLS_CERT=$2; shift 2 ;;
    --tls-key) TLS_KEY=$2; shift 2 ;;
    --package) PACKAGE=$2; shift 2 ;;
    --sha256) PACKAGE_SHA256=$2; shift 2 ;;
    --signature) SIGNATURE=$2; shift 2 ;;
    --allowed-signers) ALLOWED_SIGNERS=$2; shift 2 ;;
    --update-manifest) UPDATE_MANIFEST=$2; shift 2 ;;
    --no-systemd) NO_SYSTEMD=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help)
      say "usage: install.sh --version V --package FILE|URL --sha256 HEX --allowed-signers FILE --public-origin URL"
      say "                    [--prefix DIR] [--data-dir DIR] [--config-dir DIR] [--user USER]"
      say "                    [--listen HOST:PORT] [--public-origin URL] [--tls-cert FILE --tls-key FILE]"
      say "                    [--no-systemd] [--uninstall [--purge]]"
      exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

if [ "$UNINSTALL" = 1 ]; then
  if [ "$NO_SYSTEMD" = 0 ] && command -v systemctl >/dev/null 2>&1; then
    # Every unit the installer created: the service, the update watcher and
    # the backup timer would otherwise keep firing against a missing binary.
    # One unit at a time: systemctl fails the whole call if one is missing.
    for unit in custodia-update.path custodia-backup.timer custodia.service custodia-update.service custodia-backup.service; do
      systemctl disable --now "$unit" >/dev/null 2>&1 || systemctl stop "$unit" >/dev/null 2>&1 || true
    done
    rm -f /etc/systemd/system/custodia.service /etc/systemd/system/custodia-update.service /etc/systemd/system/custodia-update.path /etc/systemd/system/custodia-backup.service /etc/systemd/system/custodia-backup.timer
    systemctl daemon-reload >/dev/null 2>&1 || true
  fi
  rm -f "$PREFIX/bin/custodia" "$PREFIX/bin/custodia.previous"
  rm -rf "$PREFIX/lib/custodia"
  if [ "$PURGE" = 1 ]; then rm -rf "$DATA_DIR" "$CONFIG_DIR"; say "Custodia removed, including data and configuration."; else say "Custodia removed. Data in $DATA_DIR and configuration in $CONFIG_DIR were preserved."; fi
  exit 0
fi

[ "$(id -u)" = 0 ] || [ "$NO_SYSTEMD" = 1 ] || die "run as root (or use --no-systemd for a test prefix)"
[ -n "$VERSION" ] || die "--version is required"
[ -n "$PACKAGE" ] || die "--package is required (local archive or URL)"
[ -n "$PACKAGE_SHA256" ] || die "--sha256 is required"
[ -n "$ALLOWED_SIGNERS" ] || die "--allowed-signers is required"
[ -n "$PUBLIC_ORIGIN" ] || die "--public-origin is required (for example https://custodia.example:8443)"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM
# Signature and allowed_signers may also be release URLs; keep them local for
# ssh-keygen without trusting the shell's current directory.
# The manifest signature travels inside the package and is verified there;
# --signature is accepted for compatibility and not needed.
# The release key is the trust anchor: it must come from a channel other than
# the package, so the installer never downloads it.
case "$ALLOWED_SIGNERS" in
  http://*|https://*) die "--allowed-signers must be a local file obtained separately from the release" ;;
esac
[ -f "$ALLOWED_SIGNERS" ] || die "allowed signers not found: $ALLOWED_SIGNERS"
if printf '%s' "$PACKAGE" | grep -Eq '^https?://'; then
  curl -fsSL --proto '=https' --tlsv1.2 "$PACKAGE" -o "$work/package.tar.gz" || die "download failed"
else
  [ -f "$PACKAGE" ] || die "package not found: $PACKAGE"
  cp "$PACKAGE" "$work/package.tar.gz"
fi
printf '%s  %s\n' "$PACKAGE_SHA256" "$work/package.tar.gz" | sha256sum -c - >/dev/null || die "archive checksum mismatch"

# Inspect the archive before extracting it as root: only regular files and
# directories, no absolute paths and no "..".
tar -tvzf "$work/package.tar.gz" > "$work/entries" || die "release archive is unreadable"
if awk '{ t = substr($1, 1, 1); if (t != "-" && t != "d") bad = 1 } END { exit bad ? 0 : 1 }' "$work/entries"; then
  die "release archive contains links or special files"
fi
if tar -tzf "$work/package.tar.gz" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
  die "release archive contains an unsafe path"
fi
mkdir "$work/extract"
tar -xzf "$work/package.tar.gz" -C "$work/extract" --no-same-owner
packageDir=$(find "$work/extract" -maxdepth 1 -mindepth 1 -type d | head -1)
[ -n "$packageDir" ] && [ -f "$packageDir/manifest.json" ] || die "package has no manifest"
[ -f "$packageDir/manifest.json.sig" ] || die "package manifest is not signed"
ssh-keygen -Y verify -f "$ALLOWED_SIGNERS" -I custodia-release -n custodia-release -s "$packageDir/manifest.json.sig" < "$packageDir/manifest.json" >/dev/null || die "release signature is invalid"
grep -q "\"version\": \"$VERSION\"" "$packageDir/manifest.json" || die "manifest version does not match --version"
grep -q "\"architecture\": \"$ARCH\"" "$packageDir/manifest.json" || die "manifest architecture does not match this host"
# The signature covers manifest.json only, so every extracted file must match
# the signed list (path and SHA-256) and nothing unlisted may be installed.
# package-go.mjs writes one "path" and one "sha256" line per file.
awk -F'"' '/"path":/ { path = $4 } /"sha256":/ { print $4 "  " path }' "$packageDir/manifest.json" > "$work/files.sha256"
[ -s "$work/files.sha256" ] || die "signed manifest lists no files"
(cd "$packageDir" && sha256sum --quiet --strict -c "$work/files.sha256") >/dev/null 2>&1 || die "package files do not match the signed manifest"
cut -c67- "$work/files.sha256" | sort > "$work/listed"
(cd "$packageDir" && find . -type f ! -path ./manifest.json ! -path ./manifest.json.sig | sed 's#^\./##' | sort) > "$work/present"
cmp -s "$work/listed" "$work/present" || die "package contains files not listed in the signed manifest"

if [ "$NO_SYSTEMD" = 0 ]; then
  id "$SERVICE_USER" >/dev/null 2>&1 || useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
fi
install -d -m 0755 "$PREFIX/bin" "$PREFIX/lib/custodia"
# init creates the data directory itself (0700); it refuses an existing one.
# The configuration directory stays owned by root: the service user can read
# its key and TLS files but cannot replace them.
install -d -m 0750 -o root -g "$SERVICE_USER" "$CONFIG_DIR" 2>/dev/null || install -d -m 0750 "$CONFIG_DIR"
install -m 0755 "$packageDir/custodia" "$PREFIX/bin/custodia"
rm -rf "$PREFIX/lib/custodia/web"
cp -R "$packageDir/web" "$PREFIX/lib/custodia/web"
chmod -R a+rX "$PREFIX/lib/custodia/web"
[ -f "$packageDir/LICENSE" ] && install -m 0644 "$packageDir/LICENSE" "$PREFIX/lib/custodia/LICENSE"
# Pin the release key that verified this package. The root updater trusts only
# this file, which the service user cannot write; a console request cannot
# bring its own key.
install -m 0644 "$ALLOWED_SIGNERS" "$PREFIX/lib/custodia/allowed_signers"

# TLS: provided files are copied with 0600; otherwise generate a self-signed
# certificate with SANs for the hostname and any literal IP in the origin.
mkdir -p "$CONFIG_DIR/tls"
if [ -n "$TLS_CERT" ] && [ -n "$TLS_KEY" ]; then
  install -m 0644 "$TLS_CERT" "$CONFIG_DIR/tls/server.crt"
  install -m 0600 "$TLS_KEY" "$CONFIG_DIR/tls/server.key"
else
  host=$(printf '%s' "$PUBLIC_ORIGIN" | sed -E 's#^https?://##; s#[:/].*##')
  san="DNS:$host"
  if printf '%s' "$host" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then san="IP:$host"; fi
  openssl req -x509 -newkey rsa:2048 -nodes -days 825 -subj "/CN=$host" -addext "subjectAltName=$san" -keyout "$CONFIG_DIR/tls/server.key" -out "$CONFIG_DIR/tls/server.crt" >/dev/null 2>&1
  chmod 0600 "$CONFIG_DIR/tls/server.key"
  chmod 0644 "$CONFIG_DIR/tls/server.crt"
fi

# Initialize the database and master key only once; an upgrade keeps both.
if [ ! -f "$DATA_DIR/state.db" ]; then
  "$PREFIX/bin/custodia" init --dir "$DATA_DIR" >/dev/null
fi
# The key is generated next to the database and then moved out of it; the
# installer never regenerates or overwrites an existing key.
if [ ! -f "$CONFIG_DIR/master.key" ] && [ -f "$DATA_DIR/master.key" ]; then
  mv "$DATA_DIR/master.key" "$CONFIG_DIR/master.key"
fi
chmod 0600 "$CONFIG_DIR/master.key" 2>/dev/null || true
first_admin=0
if [ ! -f "$DATA_DIR/.admin-created" ]; then
  password=$(openssl rand -base64 24 | tr -d '\n')
  if printf '%s\n' "$password" | "$PREFIX/bin/custodia" create-admin --dir "$DATA_DIR" --username admin --display-name "Custodia Admin" --connections "${CUSTODIA_ADMIN_CONNECTIONS:-}" --must-change >/dev/null 2>&1; then
    touch "$DATA_DIR/.admin-created"
    chmod 0600 "$DATA_DIR/.admin-created"
    first_admin=1
  else
    password=
  fi
fi

if [ "$NO_SYSTEMD" = 0 ]; then
  cat > /etc/systemd/system/custodia.service <<UNIT
[Unit]
Description=Custodia self-hosted inventory
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
ExecStart=$PREFIX/bin/custodia serve --auth-mode local --public-origin $PUBLIC_ORIGIN --dir $DATA_DIR --web-dir $PREFIX/lib/custodia/web --listen $LISTEN --tls-cert $CONFIG_DIR/tls/server.crt --tls-key $CONFIG_DIR/tls/server.key --master-key $CONFIG_DIR/master.key${UPDATE_MANIFEST:+ --update-manifest $UPDATE_MANIFEST}
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DATA_DIR $CONFIG_DIR
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
UNIT
  install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR/.update" 2>/dev/null || true
  # The updater checks health on the local listener and verifies the
  # certificate against the public host it was issued for.
  health_port=${LISTEN##*:}
  health_host=$(printf '%s' "$PUBLIC_ORIGIN" | sed -E 's#^https?://##; s#[:/].*##')
  cat > /etc/systemd/system/custodia-update.service <<UNIT
[Unit]
Description=Custodia signed update
After=network-online.target

[Service]
Type=oneshot
ExecStart=$PREFIX/bin/custodia update --request $DATA_DIR/.update/request.json --allowed-signers $PREFIX/lib/custodia/allowed_signers --dir $DATA_DIR --binary $PREFIX/bin/custodia --web-dir $PREFIX/lib/custodia/web --service custodia.service --health-url https://127.0.0.1:$health_port/api/health --health-ca $CONFIG_DIR/tls/server.crt --health-server-name $health_host
UNIT
  cat > /etc/systemd/system/custodia-update.path <<UNIT
[Unit]
Description=Watch for Custodia update requests

[Path]
PathExists=$DATA_DIR/.update/request.json
Unit=custodia-update.service

[Install]
WantedBy=multi-user.target
UNIT
  install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR/backups" 2>/dev/null || true
  cat > /etc/systemd/system/custodia-backup.service <<UNIT
[Unit]
Description=Custodia daily SQLite backup

[Service]
Type=oneshot
User=$SERVICE_USER
Group=$SERVICE_USER
ExecStart=/bin/sh -c 'exec $PREFIX/bin/custodia backup --dir $DATA_DIR --out $DATA_DIR/backups/custodia-\$\$(date -u +%%Y%%m%%d-%%H%%M%%S).db'
UNIT
  cat > /etc/systemd/system/custodia-backup.timer <<UNIT
[Unit]
Description=Daily Custodia backup

[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true

[Install]
WantedBy=timers.target
UNIT
  chown -R "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR"
  # Root keeps /etc/custodia and the release key; the service only owns the
  # secrets it must read.
  chown root:"$SERVICE_USER" "$CONFIG_DIR" "$CONFIG_DIR/tls"
  chmod 0750 "$CONFIG_DIR" "$CONFIG_DIR/tls"
  chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR/master.key" "$CONFIG_DIR/tls/server.key" "$CONFIG_DIR/tls/server.crt"
  chown root:root "$PREFIX/lib/custodia/allowed_signers"
  systemctl daemon-reload
  systemctl enable custodia.service custodia-update.path custodia-backup.timer >/dev/null
  systemctl start custodia-backup.timer >/dev/null 2>&1 || true
  systemctl start custodia-update.path >/dev/null 2>&1 || true
  systemctl restart custodia.service
  sleep 2
  code=$(curl -ks -o /dev/null -w '%{http_code}' "https://127.0.0.1:${LISTEN##*:}/api/health" || true)
  [ "$code" = 200 ] || die "service started but health returned $code"
fi

say "Custodia $VERSION installed."
say "  binary: $PREFIX/bin/custodia"
say "  data:   $DATA_DIR"
say "  config: $CONFIG_DIR"
say "  URL:    $PUBLIC_ORIGIN"
if [ "$first_admin" = 1 ]; then
  say "  first administrator: admin"
  say "  one-time password: $password"
  say "  Change it at the first sign-in. It is not logged; this is the only print."
fi
if [ "$NO_SYSTEMD" = 1 ]; then
  say "Run: $PREFIX/bin/custodia serve --auth-mode local --public-origin $PUBLIC_ORIGIN --dir $DATA_DIR --web-dir $PREFIX/lib/custodia/web --listen $LISTEN --tls-cert $CONFIG_DIR/tls/server.crt --tls-key $CONFIG_DIR/tls/server.key --master-key $CONFIG_DIR/master.key"
fi
exit 0
