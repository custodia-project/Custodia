#!/bin/sh
# Real systemd acceptance in a disposable LXD system container.
#
# Builds two signed releases (N-1 and N) with a throwaway release key, then in
# a fresh Ubuntu container: installs N-1 as root with systemd, re-runs the
# installer (idempotency), signs in with the one-time password, changes it,
# registers a connection with an encrypted secret, takes a console backup,
# plants a link from the data directory to a root-owned canary, requests the
# update from the console and lets custodia-update.path apply it, then tries a
# forged update signed with the service user's own key, runs the backup timer
# service, uninstalls, reinstalls and purges. The container is always deleted.
#
# Requires: lxc (LXD) with an Ubuntu 24.04 image, node, go, ssh-keygen.
# Usage: sh scripts/accept-systemd-lxd.sh [image]   (default: ubuntu:24.04)
set -eu

IMAGE=${1:-ubuntu:24.04}
NAME=custodia-accept-$$
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'lxc delete -f "$NAME" >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT INT TERM

fail() { echo "ACCEPTANCE FAILED: $*" >&2; exit 1; }
in_c() { lxc exec "$NAME" -- sh -c "$1"; }

ssh-keygen -q -t ed25519 -N '' -f "$WORK/release-key" -C custodia-accept
printf 'custodia-release %s\n' "$(cut -d' ' -f1,2 "$WORK/release-key.pub")" > "$WORK/allowed_signers"
for v in 0.1.0 0.2.0; do
  (cd "$ROOT" && CUSTODIA_VERSION=$v node scripts/package-go.mjs --out "$WORK/out" --signing-key "$WORK/release-key" >/dev/null)
done
SHA1=$(sha256sum "$WORK/out/custodia-0.1.0-linux-amd64.tar.gz" | cut -d' ' -f1)
SHA2=$(sha256sum "$WORK/out/custodia-0.2.0-linux-amd64.tar.gz" | cut -d' ' -f1)
printf '{"version":"0.2.0","package":"/srv/release/custodia-0.2.0-linux-amd64.tar.gz","sha256":"%s","signature":"/srv/release/custodia-0.2.0.manifest.json.sig"}\n' "$SHA2" > "$WORK/release.json"

lxc launch "$IMAGE" "$NAME" >/dev/null
state=
for _ in $(seq 1 60); do
  state=$(lxc exec "$NAME" -- systemctl is-system-running 2>/dev/null || true)
  case "$state" in running|degraded) break ;; esac
  sleep 2
done
case "$state" in running|degraded) ;; *) fail "systemd did not start ($state)" ;; esac
IP=$(lxc list "$NAME" -c 4 --format csv | cut -d' ' -f1)
[ -n "$IP" ] || fail "container has no IPv4 address"
ORIGIN=https://$IP:8443

in_c 'mkdir -p /srv/release /root/install'
lxc file push "$ROOT/scripts/install.sh" "$WORK/allowed_signers" "$NAME/root/install/" >/dev/null
lxc file push "$WORK/out/custodia-0.1.0-linux-amd64.tar.gz" "$WORK/out/custodia-0.2.0-linux-amd64.tar.gz" "$WORK/release.json" "$NAME/srv/release/" >/dev/null
lxc file push "$WORK/out/custodia-0.2.0-linux-amd64/manifest.json.sig" "$NAME/srv/release/custodia-0.2.0.manifest.json.sig" >/dev/null
in_c 'chmod 0755 /srv/release; chmod 0644 /srv/release/*; chown root:root /root/install/allowed_signers; chmod 0644 /root/install/allowed_signers'

INSTALL="sh /root/install/install.sh --signature unused --allowed-signers /root/install/allowed_signers --public-origin $ORIGIN --update-manifest /srv/release/release.json"

echo "1. install N-1 as root with systemd"
in_c "$INSTALL --version 0.1.0 --package /srv/release/custodia-0.1.0-linux-amd64.tar.gz --sha256 $SHA1 > /root/install.log 2>&1" || fail "installer exited non-zero"
in_c "sed -nE 's/.*one-time password: (\S+).*/\1/p' /root/install.log > /root/temp-pass; chmod 600 /root/temp-pass; test -s /root/temp-pass" || fail "no one-time password"
in_c 'systemctl is-active -q custodia.service custodia-update.path custodia-backup.timer' || fail "units not active"
in_c 'curl -ks https://127.0.0.1:8443/api/health | grep -q "\"version\":\"0.1.0\""' || fail "health"
in_c 'test "$(stat -c "%U:%G %a" /etc/custodia)" = "root:custodia 750"' || fail "/etc/custodia ownership"
in_c 'test "$(stat -c "%U:%G %a" /etc/custodia/master.key)" = "custodia:custodia 600"' || fail "master key ownership"
in_c 'test "$(stat -c "%U:%G %a" /usr/local/lib/custodia/allowed_signers)" = "root:root 644"' || fail "release key ownership"
in_c 'test ! -e /var/lib/custodia/master.key' || fail "master key left in the data directory"
in_c 'test "$(ps -o user= -C custodia | sort -u)" = "custodia"' || fail "service does not run as custodia"

echo "2. re-run the installer (idempotent)"
in_c 'md5sum /etc/custodia/master.key > /root/key.md5'
in_c "$INSTALL --version 0.1.0 --package /srv/release/custodia-0.1.0-linux-amd64.tar.gz --sha256 $SHA1 > /root/reinstall.log 2>&1" || fail "re-install exited non-zero"
in_c '! grep -q "one-time password" /root/reinstall.log && md5sum -c --quiet /root/key.md5' || fail "re-install changed the admin or the key"

echo "3. console: sign in, change password, secret connection, backup, update"
cat > "$WORK/flow.sh" <<FLOW
set -eu
O=$ORIGIN
J=/root/jar
c() { curl -ks -b \$J -c \$J "\$@"; }
csrf() { c \$O/api/auth/status | sed -E 's/.*"csrf_token":"([^"]+)".*/\1/'; }
P=\$(cat /root/temp-pass)
c -H "Origin: \$O" -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"\$P\"}" \$O/api/auth/login | grep -q '"must_change_password":true'
T=\$(csrf)
c -H "Origin: \$O" -H "X-Custodia-CSRF: \$T" -H 'Content-Type: application/json' -d "{\"current_password\":\"\$P\",\"new_password\":\"Systemd acceptance passphrase 17\"}" \$O/api/auth/password | grep -q '"changed":true'
T=\$(csrf)
c -H "Origin: \$O" -H "X-Custodia-CSRF: \$T" -H 'Content-Type: application/json' -d '{"connector":"cyberark-pam-safes-v1","base_url":"https://pam.acme.example","collections":["safes"],"scope":"accept","auth_mode":"bearer","secret":"accept-secret-value"}' \$O/api/ingestion/connections | grep -q '"group_id"'
! grep -q accept-secret-value /var/lib/custodia/state.db
c -H "Origin: \$O" -H "X-Custodia-CSRF: \$T" -H 'Content-Type: application/json' -d '{}' \$O/api/system/backups | grep -q '"name"'
printf 'canary\n' > /etc/custodia-canary; chmod 0644 /etc/custodia-canary
runuser -u custodia -- ln -sf /etc/custodia-canary /var/lib/custodia/state.db.update-backup
c \$O/api/system/version | grep -q '"update_available":true'
c -H "Origin: \$O" -H "X-Custodia-CSRF: \$T" -H 'Content-Type: application/json' -d '{}' \$O/api/system/update | grep -q '"requested":true'
FLOW
lxc file push "$WORK/flow.sh" "$NAME/root/flow.sh" >/dev/null
in_c 'sh /root/flow.sh' || fail "console flow"
in_c 'for i in $(seq 1 60); do sleep 2; [ -e /var/lib/custodia/.update/request.json ] || systemctl is-active -q custodia-update.service || break; done'
in_c 'test "$(systemctl show custodia-update.service -p Result --value)" = success' || fail "update service failed"
in_c 'test "$(/usr/local/bin/custodia version)" = 0.2.0 && curl -ks https://127.0.0.1:8443/api/health | grep -q "\"version\":\"0.2.0\""' || fail "update did not reach N"
in_c 'test "$(cat /etc/custodia-canary)" = canary && test "$(stat -c %U /etc/custodia-canary)" = root' || fail "root followed a link planted in the data directory"
in_c 'test ! -e /var/lib/custodia/.update/request.json' || fail "update request left behind"
in_c "curl -ks -c /root/jar2 -H 'Origin: $ORIGIN' -H 'Content-Type: application/json' -d '{\"username\":\"admin\",\"password\":\"Systemd acceptance passphrase 17\"}' $ORIGIN/api/auth/login | grep -q '\"authenticated\":true' && curl -ks -b /root/jar2 $ORIGIN/api/ingestion/connections | grep -q '\"secret_configured\":true'" || fail "admin or secret lost after the update"

echo "4. forged update from the service user is rejected under real root"
in_c 'runuser -u custodia -- sh -ec "
cd /tmp && rm -rf ev && mkdir ev && cd ev
ssh-keygen -q -t ed25519 -N \"\" -f key
printf \"custodia-release %s\n\" \"\$(cut -d\" \" -f1,2 key.pub)\" > signers
tar -xzf /srv/release/custodia-0.2.0-linux-amd64.tar.gz
d=custodia-0.2.0-linux-amd64
sed -i \"s/\\\"version\\\": \\\"0.2.0\\\"/\\\"version\\\": \\\"0.9.0\\\"/\" \$d/manifest.json
printf \"#!/bin/sh\\necho pwned > /tmp/pwned\\n\" > \$d/custodia
rm -f \$d/manifest.json.sig; ssh-keygen -Y sign -f key -n custodia-release \$d/manifest.json >/dev/null 2>&1
tar -czf evil.tar.gz \$d
sha=\$(sha256sum evil.tar.gz | cut -d\" \" -f1)
printf \"{\\\"manifest\\\":{\\\"version\\\":\\\"0.9.0\\\",\\\"package\\\":\\\"/tmp/ev/evil.tar.gz\\\",\\\"sha256\\\":\\\"%s\\\",\\\"signature\\\":\\\"/tmp/ev/\$d/manifest.json.sig\\\",\\\"allowed_signers\\\":\\\"/tmp/ev/signers\\\"}}\" \$sha > /var/lib/custodia/.update/request.json
"'
in_c 'for i in $(seq 1 30); do sleep 2; [ -e /var/lib/custodia/.update/request.json ] || systemctl is-active -q custodia-update.service || break; done; sleep 2'
in_c 'journalctl -u custodia-update.service --no-pager | grep -q "release signature rejected"' || fail "forged update not rejected by its signature"
in_c 'test "$(/usr/local/bin/custodia version)" = 0.2.0 && test ! -e /tmp/pwned && test ! -e /var/lib/custodia/.update/request.json' || fail "forged update changed the host or looped"

echo "4b. a request naming a root-only file is read with the service user's rights"
in_c "runuser -u custodia -- sh -c 'printf \"{\\\"manifest\\\":{\\\"version\\\":\\\"0.9.1\\\",\\\"package\\\":\\\"/etc/shadow\\\",\\\"sha256\\\":\\\"00\\\",\\\"signature\\\":\\\"/etc/shadow\\\"}}\" > /var/lib/custodia/.update/request.json'"
in_c 'for i in $(seq 1 30); do sleep 2; [ -e /var/lib/custodia/.update/request.json ] || systemctl is-active -q custodia-update.service || break; done; sleep 2'
in_c 'journalctl -u custodia-update.service --no-pager | grep -q "update request file: .*Permission denied"' || fail "root opened a file chosen by the service user"
in_c 'test "$(/usr/local/bin/custodia version)" = 0.2.0' || fail "root-only request changed the host"

echo "5. backup timer service"
in_c 'systemctl start custodia-backup.service && test "$(systemctl show custodia-backup.service -p Result --value)" = success' || fail "backup timer service"
in_c 'ls /var/lib/custodia/backups | grep -Eq "^custodia-[0-9]{8}-[0-9]{6}\.db$"' || fail "backup file name"

echo "6. uninstall keeps data, every unit stops; purge removes data"
in_c "sh /root/install/install.sh --uninstall >/dev/null" || fail "uninstall exited non-zero"
in_c 'for u in custodia.service custodia-update.path custodia-backup.timer; do ! systemctl is-active -q $u || exit 1; done' || fail "a unit is still active after uninstall"
in_c 'test -f /var/lib/custodia/state.db && test -f /etc/custodia/master.key' || fail "uninstall removed data"
in_c "sh /root/install/install.sh --uninstall --purge >/dev/null && test ! -e /var/lib/custodia && test ! -e /etc/custodia" || fail "purge"

echo "Systemd acceptance OK: install, idempotent re-install, one-time admin, console secret/backup/update, link-safe root updater, forged update rejected, backup timer, uninstall and purge."
