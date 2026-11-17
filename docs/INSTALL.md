---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-17
---

# Instalación, TLS y releases

Custodia se instala en Linux x86_64/arm64 con systemd. Docker queda para
después. La instalación es explícita: no hay usuario, contraseña ni token por
defecto, y el primer administrador se crea una sola vez con una contraseña
temporal impresa por el instalador y cambio obligatorio.

## Comando

```sh
curl -fsSL https://<release>/install.sh -o install.sh
sh install.sh \
  --version 0.1.0 \
  --package https://<release>/custodia-0.1.0-linux-amd64.tar.gz \
  --sha256 <hex> \
  --signature https://<release>/manifest.json.sig \
  --allowed-signers ./allowed_signers \
  --public-origin https://custodia.empresa.example:8443
```

El instalador verifica la firma del manifiesto con OpenSSH (`ssh-keygen -Y
verify`) y el SHA-256 del archivo **antes** de escribir en el host. La firma
Ed25519 se genera con una clave de release fuera del repositorio. OpenSSH se
eligió porque está disponible en los hosts Linux y no exige instalar otro
verificador; la clave privada nunca entra al repo ni a CI sin autorización.

**La clave pública es el ancla de confianza y no viaja con la release.**
`allowed_signers` se obtiene por un canal distinto del paquete (publicada en
el repositorio y en la documentación); una URL del mismo origen que el paquete
sólo probaría integridad, no autenticidad. Todavía no hay clave de release
publicada ni está embebida en el instalador o el binario: hasta entonces el
operador la provee. El instalador copia la clave que verificó a
`/usr/local/lib/custodia/allowed_signers` (root, 0644). El actualizador root
confía **sólo** en ese archivo: rechaza una clave enlazada, escribible por
grupo u otros o, cuando corre como root, que no pertenezca a root, e ignora
cualquier `allowed_signers` que traiga la solicitud de la consola. Así, quien
controle el usuario `custodia` no puede instalar un binario firmado con su
propia clave.

La firma cubre sólo `manifest.json`, por eso el manifiesto firmado manda sobre
cada archivo: instalador y actualizador comprueban que cada archivo extraído
figure en él con su tamaño y SHA-256, que no falte ninguno y que no haya
archivos de más. El SHA-256 del archivo comprimido sólo protege la descarga.
Antes de extraer, el instalador rechaza enlaces, archivos especiales, rutas
absolutas y `..`; el actualizador extrae en un directorio privado de root y
verifica esa copia. El actualizador rechaza además una release que no sea más
nueva que la instalada, para que una release firmada vieja no sirva para
volver atrás.

## Layout

| Ruta | Contenido |
|---|---|
| `/usr/local/bin/custodia` | Binario con versión embebida (`custodia version`). |
| `/usr/local/lib/custodia/web` | Frontend compilado. |
| `/usr/local/lib/custodia/allowed_signers` | Clave pública de release fijada (root, 0644); única clave que acepta el actualizador. |
| `/var/lib/custodia/state.db` | SQLite; autoridad de la instancia. |
| `/var/lib/custodia/.admin-created` | Marcador de bootstrap; no se reutiliza. |
| `/etc/custodia/` | Propiedad de root, grupo `custodia`, 0750: el servicio lee pero no reemplaza su configuración. |
| `/etc/custodia/master.key` | Clave maestra 0600 del usuario `custodia`, fuera de la base y del backup. |
| `/etc/custodia/tls/server.crt`, `server.key` | Certificado y clave TLS. |
| `/etc/systemd/system/custodia.service` | Servicio con usuario `custodia`. |

`init` crea la base vacía y `master.key`; el instalador reubica la clave a
`/etc/custodia/master.key`. Una actualización conserva base y clave. La
desinstalación conserva datos y configuración salvo `--purge`.

## TLS

`serve --tls-cert RUTA --tls-key RUTA` termina TLS en los modos `local` y
`oidc`. Sin TLS el listener sigue siendo loopback. Un certificado provisto se
copia con permisos 0600; si no se entrega uno, el instalador genera un
autofirmado con SAN del host/IP de `--public-origin`. El puerto recomendado es
8443 para no exigir puertos privilegiados; 443 requiere
`CAP_NET_BIND_SERVICE`, que la unidad declara. ACME queda fuera del MVP: un
dominio público puede usar un certificado provisto y renovarlo fuera de
Custodia.

El modo `ozal` sigue detrás del gateway y no se cambia su contraseña de
aserción. `GET /api/health` es público, sin datos, y devuelve versión y modo
para que el instalador y el actualizador comprueben salud.

## Idempotencia y desinstalación

Volver a ejecutar el instalador reemplaza binario y web, conserva la base, la
clave y el administrador existente. `--uninstall` detiene y retira el servicio
y el binario; conserva datos y configuración. `--purge` elimina también
`/var/lib/custodia` y `/etc/custodia`. La instalación real con systemd, TLS
provisto y un host descartable sigue pendiente de aceptación; el gate
`npm run verify:go:install` prueba el paquete firmado, la instalación local sin
systemd, TLS, salud, contraseña temporal, rechazo de firma inválida y
desinstalación.
