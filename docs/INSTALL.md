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
curl -fsSL https://custodia.octaviosantacruz.com/install.sh | sudo sh
```

Sin argumentos, el instalador detecta la arquitectura, lee el catálogo firmado
del canal `stable` (`/releases/stable/linux-<arch>.json` y su `.sig`), verifica
su firma con la clave de release que lleva el script, descarga el paquete que
el catálogo nombra (sólo bajo el mismo sitio) y comprueba su SHA-256. Como
dirección de la consola usa el nombre completo del host o, si no tiene, su
primera IP, en el puerto 8443, con un certificado autofirmado. Deja la consola
leyendo el mismo canal para ofrecer actualizaciones.

Opciones habituales, después de `sudo sh -s --`: `--public-origin URL`,
`--tls-cert FILE --tls-key FILE`, `--version V` (lee el catálogo firmado de esa
versión) y `--channel NOMBRE`. El modo explícito sigue disponible para hosts sin
acceso al sitio: `--package FILE|URL --sha256 HEX --version V
--allowed-signers FILE`.

El instalador verifica las firmas con OpenSSH (`ssh-keygen -Y verify`): la
del catálogo antes de descargar y la del manifiesto del paquete antes de
escribir en el host. La firma Ed25519 se genera con una clave de release fuera
del repositorio. OpenSSH se eligió porque está disponible en los hosts Linux y
no exige instalar otro verificador; la clave privada nunca entra al repo ni a
CI sin autorización.

**Modelo de confianza.** La clave pública es el ancla: viaja dentro de
`install.sh`, se publica también en `/allowed_signers` y debe coincidir con la
copia del repositorio. Por tubería, el script y su clave vienen del mismo
sitio, así que se confía en su TLS, como en otros instaladores `curl | sh`.
Quien prefiera comprobarlo descarga `install.sh` e `install.sh.sig`, verifica
la firma con la clave obtenida por otro camino y ejecuta el archivo. El
instalador copia la clave que verificó a
`/usr/local/lib/custodia/allowed_signers` (root, 0644). El actualizador root
confía **sólo** en ese archivo: rechaza una clave enlazada, escribible por
grupo u otros o, cuando corre como root, que no pertenezca a root, e ignora
cualquier `allowed_signers` que traiga la solicitud de la consola. Así, un
compromiso posterior del sitio no alcanza a servidores ya instalados, y quien
controle el usuario `custodia` no puede instalar un binario firmado con su
propia clave.

## Publicar una release en el sitio

```sh
npm run release:site -- --signing-key RUTA/clave --version 0.1.0
```

Escribe en `site/public/` (ignorado por git) `install.sh` con la clave
embebida y su firma, `allowed_signers`, los paquetes amd64 y arm64 en
`releases/v<V>/` y los catálogos firmados de esa versión y del canal. No
publica nada: el despliegue del sitio es un paso aparte y explícito.
`npm run verify:go:bootstrap` prepara estos archivos con una clave descartable,
los sirve por HTTPS local y ejecuta el comando real; rechaza un catálogo
alterado, uno que apunte fuera del sitio, un script sin clave y una versión
inexistente.

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

## Actualización desde la consola

`--update-manifest RUTA|URL` hace que el servicio instalado lea el manifiesto
de la release disponible y la consola ofrezca la actualización. La solicitud
la aplica `custodia-update.service` (root), que la consume antes de actuar:
una actualización fallida no se repite sola. Comprueba la salud en el
listener local (`--listen`) y valida el certificado contra el host de
`--public-origin` (`--health-server-name`). Backup, migración y rollback de la
base corren como el usuario dueño de `/var/lib/custodia`, nunca como root.

## Idempotencia y desinstalación

Volver a ejecutar el instalador reemplaza binario y web, conserva la base, la
clave y el administrador existente. `--uninstall` detiene y retira el servicio,
el watcher de actualización, el timer de backup y el binario; conserva datos y configuración. `--purge` elimina también
`/var/lib/custodia` y `/etc/custodia`. `npm run accept:systemd` acepta instalación, actualización, consola,
desinstalación y purga con systemd real en un contenedor LXD descartable; el gate
`npm run verify:go:install` prueba el paquete firmado, la instalación local sin
systemd, TLS, salud, contraseña temporal, rechazo de firma inválida y
desinstalación.
