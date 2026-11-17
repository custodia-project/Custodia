---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Acceso: login local, Ozal, autorización y pruebas

El modo base del producto es el login local con usuario y contraseña, con
sesión revocable en servidor (contrato abajo). El receptor de identidad
firmada de Ozal sigue disponible como integración opcional. El administrador
elige el modo al arrancar `serve`; nunca hay fallback automático entre modos.
El alta inicial del administrador es siempre un comando explícito en el host.
La integración Ozal se verifica con claves efímeras y un gateway de prueba.
OIDC genérico es un modo independiente, configurable desde la consola, con
prueba previa y administrador local de emergencia. La aceptación contra un
proveedor OIDC real, la aceptación contra una instancia real de Ozal, la
aceptación desplegada del modo local con TLS/proxy y los despliegues siguen
pendientes.

## Modos separados

| Contexto | Entrada | Autoridad de Custodia |
|---|---|---|
| Demo pública | Sin login, datos sintéticos. | Ningún acceso a inventario privado. |
| `serve --auth-mode local` | Cookie de sesión emitida tras usuario y contraseña. | Cuentas locales habilitadas, permisos por conexión y auditoría. |
| `serve --auth-mode ozal` | Aserción firmada nueva para cada petición, también assets. | Principales habilitados, permisos por conexión y auditoría. |
| `serve --auth-mode oidc` | Sesión emitida tras el flujo OIDC, o contraseña de un administrador local de emergencia. | Cuentas OIDC aprobadas, administradores locales, permisos por conexión y auditoría. |
| `serve-lab` | Bearer aleatorio de laboratorio, sólo loopback. | Principales sintéticos y concesiones explícitas. |

`serve` escucha en IP numérica loopback por defecto. En los modos `local` y
`oidc` puede terminar TLS directamente con `--tls-cert/--tls-key`; recién
entonces `--listen` admite una IP no loopback y `--public-origin` debe ser
HTTPS. Sin TLS, el listener sigue siendo sólo loopback. `serve-lab` conserva
loopback obligatorio. Escuchar en una interfaz no autentica al usuario. `--auth-mode` es obligatorio en serve y admite exactamente
`local`, `oidc` u `ozal`; no hay modo híbrido ni valor por defecto. En modo `oidc`
el login principal es el proveedor; la contraseña sólo autentica a un
administrador local creado en el host, como vía de emergencia explícita. En modo `ozal`
se exige la firma incluso con acceso directo y no se admite token de
laboratorio, cookie de sesión local, headers de usuario/email ni login local.
El modo `ozal` conserva el gateway delante y su contrato de aserción; la
terminación TLS directa no reemplaza esa verificación. En modo `local` no se registra el receptor Ozal: una aserción, un bearer o
un header de usuario no autentican; sólo la cookie de sesión. En modo `oidc`
no se registran el receptor Ozal ni el bearer de laboratorio; una aserción o
un header de identidad no autentican. Cuando falla un modo, la petición se
rechaza; no se prueba el otro. La instalación debe cerrar también los caminos
directos al backend y proteger su host.

## Salud y versión

`GET /api/health` es público, no devuelve datos ni sesiones y responde
`{status:"ok", version, mode}`. Lo usa el instalador y el actualizador para
comprobar que el proceso quedó sano después de un reinicio. `custodia version`
imprime la versión embebida en el binario.

## Modo `ozal`: contrato firmado

Se contrastó el receptor con docs/reference.md del checkout vecino de Ozal.
Ozal autentica al visitante, aplica su política y reemplaza los headers de
identidad del cliente. Entrega X-Ozal-Assertion como JWS compacto EdDSA con
clave Ed25519. Custodia verifica mediante go-jose/v4, sin criptografía propia:

- Un solo header, sin alias con guiones bajos; algoritmo permitido y kid conocido.
- Claves públicas PKIX PEM fijadas en archivos de configuración. kid es el
  hexadecimal de los primeros 16 bytes SHA-256 de la clave pública cruda.
  No se descargan claves ni se aceptan JWK, jku o x5u dentro del token.
- Emisor y audiencia exactos; sujeto opaco y jti obligatorios; iat y exp con
  duración máxima de 60 segundos y tolerancia de reloj de 5 segundos.
- jti de un solo uso por emisor, consumido transaccionalmente en SQLite antes
  de autorizar. Persiste al reiniciar y se comparte entre procesos de la base.
  Se retiene hasta exp más la tolerancia; se limpian vencidos por petición.
  El límite es 65.536 entradas: saturación o fallo de almacenamiento rechazan
  con 503, sin expulsar entradas vigentes. Replay o firma inválida da 401.

La configuración exige issuer HTTPS y public_origin HTTPS sin ruta, usuario,
query ni fragmento. Para el harness local sólo se permite HTTP en IP numérica
loopback. audience y public_key_files son explícitos. Hay un máximo de ocho
claves públicas; la rotación carga temporalmente vieja y nueva, reinicia,
cambia el emisor y luego retira la vieja/reinicia. La revocación de una clave
requiere retirarla y reiniciar todos los procesos que la cargaron.

## Modo `ozal`: primer administrador y lectores

init crea una base privada vacía: no inventario demo, usuario, contraseña ni
token. bootstrap-admin toma la configuración del emisor, un sub confirmado
fuera de banda y conexiones existentes explícitas; sin conexiones concede
sólo administración inicial. El marcador persistido permite este bootstrap
una sola vez. Crear principal, concesiones, rol y auditoría es atómico.
No se enlaza por email ni se obtiene el rol de grupos del gateway.

Cada petición resuelve emisor/sub en la base y comprueba habilitación.
Una identidad verificada pero desconocida o deshabilitada obtiene 403; nunca
se aprovisiona al entrar. El rol administrador no concede acceso universal
a las fuentes ni habilita los endpoints legacy de administración.

El administrador puede llamar POST /api/access/readers con subject y una lista
explícita connections; crea un lector del mismo emisor, con un subconjunto
de las conexiones que el administrador puede leer. Un sujeto existente falla,
sin modificar concesiones. DELETE con subject deshabilita lectores cuyo
alcance completo puede administrar; no permite deshabilitar administradores.
Ambas operaciones se auditan en la misma transacción y requieren un Origin
exactamente igual al public_origin configurado, como toda petición de escritura.
No se confía en Host ni X-Forwarded-* para esa decisión. Todavía no existe
pantalla para estas operaciones ni flujo de recuperación del administrador.

Las búsquedas, fichas y conexiones se filtran antes de correlacionar.
Cobertura requiere permiso sobre ambas fuentes y la regla explícita del par.
El sync, jobs, exportaciones y su autorización siguen pendientes.

## Modo `ozal`: cierre de sesión

POST /api/auth/logout valida identidad y Origin; devuelve gateway_logout.
El frontend envía un formulario POST al path fijo del mismo origen
/_/auth/logout. Ozal recibe su propia cookie y cierra la sesión del gateway;
Custodia no mantiene una sesión paralela que permita evadirlo. No acepta un
destino de logout indicado por la respuesta ni crea cookies de autenticación.

## Laboratorio y evidencia

init-lab agrega exclusivamente el fixture Acme y una credencial aleatoria en
archivo privado. issue-lab/revoke-lab administran credenciales sintéticas
(hash en SQLite), con concesiones e identidades separadas del emisor Ozal.
La credencial inicial también respeta habilitación y concesiones. Este modo
no es autenticación empresarial ni se activa desde serve.

verify:go prueba firma, claims, claves/rotación, replay persistido/concurrente,
saturación, bootstrap único, rollback de auditoría, CSRF, headers falsos,
permisos, revocación y ausencia de fallback. verify:go:gateway usa la UI real,
Go y SQLite con un proxy de prueba que firma cada petición: dos sujetos con
el mismo correo/grupos, alta explícita, alcance, reinicio, replay, revocación
y logout en navegador. El login del fixture es sólo una selección sintética;
no prueba passkeys, cookies o políticas de una instalación real de Ozal.
Ver [comandos y configuración](../server/README.md).

## Modo `oidc`: contrato

Custodia es **cliente OIDC** (relying party), no un servidor de identidad. El
proveedor gestiona contraseña, passkey, MFA y políticas; entrega la identidad
y Custodia conserva sesión, permisos por conexión y auditoría. Ningún nombre
de fabricante forma parte del dominio del producto: Ozal u otro proveedor
compatible pueden usarse como emisor.

Selección. `serve --auth-mode oidc --public-origin ORIGEN` con la misma regla
de ORIGEN que el modo local (HTTPS sin ruta; HTTP sólo en IP numérica de
loopback para el harness). El modo rechaza `--gateway-config`; no se registran
el receptor Ozal, el bearer de laboratorio ni headers de identidad. El login
principal es el proveedor. La contraseña sólo autentica a un administrador
local creado en el host, como vía de emergencia explícita: no hay fallback
automático, botón silencioso ni modo híbrido. Si no existe un administrador
local, `serve` lo advierte al arrancar.

Configuración. Se administra desde la consola (`GET/PUT /api/access/oidc`),
sólo por un administrador, con CSRF y auditoría. Campos: emisor exacto,
client ID, referencia del secreto, URL de retorno, scopes, claim de sujeto y
claims de email y nombre. La URL de retorno debe ser exactamente
`PUBLIC_ORIGIN/api/auth/oidc/callback`. El emisor debe ser HTTPS sin query,
fragmento ni barra final; HTTP sólo en IP numérica de loopback para pruebas.
Guardar crea o reemplaza un **borrador**: desactiva el proveedor, borra la
prueba anterior y revoca las sesiones OIDC vivas. Una identidad OIDC sólo se
emite cuando se activa una configuración probada.

Secreto. `client_secret_ref` admite `env:NOMBRE`, `file:/RUTA` o
`secret:NOMBRE`. Las formas `file:` y `secret:` leen únicamente dentro de
`CUSTODIA_OIDC_SECRET_DIR`; no se acepta una ruta arbitraria del host. El
valor se lee en memoria al iniciar el flujo; nunca se guarda, audita, devuelve
ni aparece en logs. Un cliente público puede dejar la referencia vacía.

Prueba y activación. `POST /api/access/oidc/test` lee el descubrimiento del
emisor, exige que `issuer` coincida exactamente, que los endpoints sean HTTPS
(o loopback numérico) y que el JWKS publique al menos una clave asimétrica de
firma; además comprueba que el flujo `code` esté anunciado. No simula un login
interactivo. `POST /api/access/oidc/enable` exige una prueba correcta de la
configuración vigente hecha en los últimos diez minutos; además revoca las
sesiones locales de lectores. `disable` la desactiva y revoca las sesiones
OIDC. Cada cambio y prueba se audita.

Flujo. `GET /api/auth/oidc/start` exige un proveedor activo y redirige con
Authorization Code + PKCE S256. `state` y `nonce` son aleatorios de un solo
uso; SQLite guarda sólo el SHA-256 de `state`, con vencimiento de diez minutos,
y una cookie `HttpOnly`, `Secure`, `SameSite=Lax` y `Path` del callback liga
la solicitud al navegador. El callback exige que la cookie y el `state` de la
query coincidan, consume el estado antes de canjear el código y rechaza
repeticiones. El backend canjea el código con el verificador PKCE y valida el
ID token con `github.com/coreos/go-oidc/v3` (Apache 2.0) sobre
`golang.org/x/oauth2` (BSD-3): firma con las claves del emisor, `iss`, `aud`,
`exp` y `nonce`; `azp` cuando corresponde. No autentica con un access token
sin validar su propósito ni acepta un emisor elegido por el navegador. Los
errores vuelven a la interfaz como códigos estables (`oidc_state`,
`oidc_token`, `oidc_exchange`, `oidc_pending`, etc.), nunca como tokens o
detalles del proveedor. `verify:go:oidc` recorre el flujo completo en Chromium
contra Go, SQLite y un proveedor falso local.

Identidad y alta. La identidad persistida es emisor + sujeto, nunca sólo
email. Un `sub` puede mapearse desde otro claim, pero debe ser estable y no
vacío; email y nombre son sólo presentación y no se usa `email_verified` para
autorizar. Una identidad válida desconocida entra en una cola de aprobación
(máximo 1.024), sin principal ni concesión. Un administrador la aprueba con
`POST /api/access/oidc/pending/approve` indicando emisor, sujeto y un
subconjunto de sus propias conexiones; la creación del principal, sus
concesiones y la auditoría son atómicas. Rechazarla la elimina. No hay
aprovisionamiento automático, enlace por email con cuentas locales u Ozal, ni
roles o grupos heredados del proveedor. Una cuenta deshabilitada obtiene 403
y no puede iniciar sesión.

Sesión. Tras validar el ID token, Custodia emite una sesión opaca de servidor
con el mismo contrato que el modo local: token de 32 bytes, sólo SHA-256 en
SQLite, cookie `HttpOnly`/`Secure`/`SameSite=Strict`, 12 horas absolutas y 30
minutos de inactividad, y CSRF por header más Origin exacto en mutaciones.
Deshabilitar la cuenta, revocar sesiones, cambiar la configuración, desactivar
el proveedor o cerrar sesión las elimina. El logout es local: no cierra la
sesión del proveedor ni redirige a un destino elegido por la respuesta.

Emergencia. Si el proveedor falla o está mal configurado, el administrador
local entra por el formulario de contraseña de la misma pantalla, bajo el
rótulo explícito de acceso de administrador local. Ese formulario usa el mismo
throttling, errores genéricos y política de contraseñas del modo local; una
cuenta local que no sea administradora no puede usarlo. En modo OIDC sólo se
acepta la sesión local persistida de ese administrador: una cookie local de
lector se rechaza aunque su fila todavía exista. La recuperación sigue siendo
`reset-password` en el host.

Límites de esta entrega. No hay MFA propio (lo aporta el proveedor), mapeo de
grupos a roles, edición de concesiones después de aprobar, logout iniciado por
el proveedor, múltiples emisores simultáneos ni aprovisionamiento por SCIM.
La prueba de configuración no completa un login interactivo. La aceptación
contra un proveedor OIDC real y el despliegue con TLS/proxy siguen pendientes.

Validación. Pruebas Go con un proveedor simulado que firma RS256 y publica
descubrimiento y JWKS: guardar, probar, activar y desactivar; activar sin
prueba o con prueba vieja falla; state ausente, repetido o vencido; cookie de
state distinta; nonce y audiencia incorrectos; identidad desconocida a la cola;
aprobación con conexiones fuera del alcance del administrador; sesión, logout,
deshabilitado con revocación, cambio de configuración con revocación y
administrador local de emergencia. La prueba e2e de la web cubre el botón del
proveedor y la consola de configuración, prueba, activación y aprobación con
la API simulada. Falta repetir el recorrido contra un proveedor real.

Referencias: [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html)
y [OAuth Security Best Current Practice](https://www.rfc-editor.org/rfc/rfc9700.html).

## Modo `local`: contrato

Selección. `serve --auth-mode local --public-origin ORIGEN`. ORIGEN sigue la
regla de public_origin de Ozal: HTTPS sin ruta, usuario, query ni fragmento;
HTTP sólo en IP numérica loopback para el harness. El modo local rechaza
`--gateway-config` y el modo ozal rechaza `--public-origin`. Cambiar de modo
es reiniciar el proceso con otro valor; las cuentas de cada modo viven en
emisores distintos (`custodia-local` frente al issuer de Ozal) y nunca se
enlazan entre sí, ni por email ni por nombre.

Cuentas. Una cuenta local es un principal con emisor `custodia-local` y
sujeto igual al nombre de usuario normalizado: minúsculas, 1 a 128
caracteres de `a-z 0-9 . _ @ -`, empezando por letra o dígito. Reutiliza
habilitación, rol administrador, concesiones por conexión y auditoría de los
principales existentes. No hay registro abierto, primer visitante
administrador ni credenciales por defecto: `init` crea una base sin cuentas.

Contraseñas. Entre 15 y 256 caracteres, sin reglas de composición; distinta
del usuario y de la contraseña anterior. Se guardan sólo como argon2id
(`golang.org/x/crypto/argon2`) en formato PHC con versión 19, memoria
64 MiB, 3 iteraciones, paralelismo 2, sal aleatoria de 16 bytes y salida de
32 bytes. La verificación compara en tiempo constante y rechaza hashes con
parámetros fuera de rango. Un usuario inexistente consume el mismo cálculo
contra un hash ficticio. Hay como máximo cuatro cálculos simultáneos por
proceso. La contraseña nunca se registra, audita, devuelve ni se acepta como
argumento de línea de comandos.

Sesión. Login exitoso crea un token aleatorio de 32 bytes; SQLite guarda sólo
su SHA-256, el principal, creación, última actividad y vencimiento. La fila de
sesión se inserta condicionada al hash que se acaba de verificar, en la misma
transacción: un cambio de contraseña concurrente no puede revivir la credencial
anterior. Cookie `__Host-custodia_session` (o `custodia_session` con origen HTTP
loopback) con `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`, sin dominio ni
Max-Age. Expiración absoluta 12 horas e inactividad 30 minutos, verificadas en el
servidor en cada petición; la actividad se registra con resolución de un
minuto. Cada login y cambio de contraseña emite un token nuevo; el reseteo por
host revoca todas las sesiones y el siguiente login con la contraseña temporal
emite el token nuevo. Deshabilitar la cuenta, resetear o cambiar la contraseña,
el logout y la revocación administrativa borran las sesiones afectadas en la
misma transacción. Una sesión ausente, vencida o revocada da 401.

CSRF. Toda petición distinta de GET/HEAD exige un único Origin exactamente
igual a ORIGEN, sin confiar en Host ni X-Forwarded-*. Con sesión, además, el
header `X-Custodia-CSRF` debe coincidir con el token CSRF de la sesión
(derivado del token de sesión con SHA-256 y comparado en tiempo constante).
`/api/auth/status` y el login devuelven ese token; el navegador no lo guarda
fuera de memoria. Falta o error de Origin/CSRF da 403 sin efectos.

Login. `POST /api/auth/login` con JSON `{username,password}`, cuerpo máximo
8 KiB y campos desconocidos rechazados. Usuario inexistente y contraseña
incorrecta dan el mismo 401 «Usuario o contraseña incorrectos.». Una cuenta
deshabilitada con contraseña correcta da 403. Antes de calcular el hash se
reservan atómicamente las dos claves persistidas en SQLite: 5 fallos por
usuario o 50 por IP del socket en 15 minutos bloquean esa clave 15 minutos con
429; el bloqueo aplica también a usuarios inexistentes, por lo que no los
revela. La reserva impide que peticiones concurrentes superen el límite antes
de que se registre el primer fallo. Detrás de un proxy en loopback la IP es la
del proxy y su límite actúa como freno global. Un login exitoso limpia el
contador del usuario. Si la tabla de intentos llega a 65.536 claves vigentes,
el login falla cerrado con 429.

Estado. `GET /api/auth/status` sin sesión válida responde 200 con
`authenticated:false`, `mode:"local"` y sin permisos; con sesión, el mismo
contrato que Ozal más `csrf_token` y `user.must_change_password`. Una cuenta
con contraseña temporal sólo puede consultar estado, cambiar la contraseña y
cerrar sesión; el resto de la API da 403. Los assets de la interfaz se
sirven sin sesión porque no contienen datos; las rutas `/api/` que no son
`/api/auth/status` ni el login exigen sesión. El administrador local anuncia
`access:local_users_manage` para la vista mínima de cuentas.

Cambio de contraseña. `POST /api/auth/password` con
`{current_password,new_password}` y CSRF. Verifica la actual (los fallos
cuentan para el límite del usuario), aplica la política, reemplaza el hash
condicionado al anterior, quita la marca temporal, revoca todas las sesiones
de la cuenta y emite una nueva para el navegador que hizo el cambio.

Logout. `POST /api/auth/logout` con CSRF borra la sesión, expira la cookie y
responde `{"logged_out":true}`; nunca `gateway_logout`.

Administración mínima (sólo administrador local, CSRF, auditada en la misma
transacción, sin contraseñas en auditoría ni respuestas):

- `GET /api/access/users`: cuentas locales con estado, rol, marca temporal,
  último login, sesiones activas y conexiones concedidas.
- `POST /api/access/users` con `{username,display_name,password,connections}`:
  crea un lector con una contraseña temporal elegida por el administrador y
  entregada fuera de banda; las conexiones deben ser un subconjunto de las
  del administrador. Un usuario existente falla sin cambios.
- `POST /api/access/users/disable` y `/enable` con `{username}`: sólo
  lectores cuyo alcance completo administra; deshabilitar revoca sesiones.
  No permite deshabilitar administradores ni a sí mismo.
- `POST /api/access/users/revoke-sessions` con `{username}`: lectores en su
  alcance o la propia cuenta.

Las concesiones por conexión son las existentes; esta API no las edita
después del alta. El administrador local anuncia `access:local_users_manage`
(capacidad de interfaz, no una concesión de fuente) para la vista mínima de
cuentas, que no usa el directorio ni los roles heredados. Roles adicionales,
edición de concesiones y reseteo de contraseña ajena desde la consola quedan
pendientes.

Host. `create-admin --dir D --username U --display-name N [--connections
a,b] [--password-file F]` crea el primer administrador local una sola vez
(marcador persistido), atómico con su auditoría. `reset-password --dir D
--username U [--password-file F]` es la recuperación: fija una contraseña
temporal, revoca sesiones, limpia el bloqueo del usuario y audita con actor
`host`; no cambia la habilitación. Ambos leen la contraseña de la primera
línea de F (archivo regular, no legible por grupo ni otros) o de la entrada
estándar; nunca de un argumento.

### Primer administrador con código de configuración (17/11)

Una instalación nueva no imprime contraseñas. Mientras no exista el primer
administrador local, el host emite un **código de configuración** y la persona
crea su cuenta en el navegador eligiendo su propia contraseña. Modelo tomado
del alta por código de Ozal, sin su seguimiento en vivo desde la terminal.

- **Emisión, sólo en el host.** `custodia setup-code --dir D` (lo ejecuta el
  instalador al terminar) genera un código de 8 símbolos del alfabeto
  `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (sin `I`, `O`, `0` ni `1`; 40 bits) y lo
  imprime una vez como `XXXX-XXXX`. Se guarda sólo su SHA-256, con emisión y
  vencimiento a los 15 minutos. Emitir un código nuevo invalida los anteriores
  (como mucho uno vigente). Se rechaza si ya existe el primer administrador.
  Nunca se escribe en logs ni en respuestas; la auditoría registra la emisión
  sin el código.
- **Estado.** `GET /api/auth/status` sin sesión informa `setup_available: true`
  sólo en modo `local`, mientras no hay administrador y hay un código vigente.
  No revela nada más.
- **Canje.** `POST /api/auth/setup` con `{code, username, display_name,
  password}`, con el mismo control de `Origin` que el login. Acepta el código
  con o sin guion y en minúsculas. En una sola transacción comprueba que no
  exista el primer administrador, que el código coincida con el vigente y no
  esté vencido ni usado, crea la cuenta como administrador con el marcador de
  bootstrap (sin cambio obligatorio: la contraseña la eligió la persona y nunca
  se imprimió), marca el código como usado y audita `setup_admin_created`.
  Luego inicia la sesión como un login normal. La contraseña cumple la misma
  política que el resto de las cuentas.
- **Intentos.** Un código equivocado no destruye el vigente (nadie arruina la
  instalación de otro adivinando). Se limitan los fallos por IP (5 cada 15
  minutos, con bloqueo de 15) y en total (30 cada 15 minutos, que pausan todos
  los canjes durante 5 minutos, menos que la vigencia del código, para que su
  dueño todavía pueda usarlo). Emitir un código nuevo desde el host limpia los
  contadores: quien adivina desde afuera no puede impedir la configuración.
  Con 40 bits y esos límites, la probabilidad de acertar por fuerza bruta
  dentro de la vigencia es despreciable. Las respuestas no distinguen código inexistente,
  vencido o usado.
- **Cierre.** Con el primer administrador creado, `setup-code` y el canje se
  rechazan y `setup_available` es falso. `create-admin` sigue disponible para
  automatización y `reset-password` para recuperación, ambos en el host.

El primero en escribir un código válido crea la cuenta: no hay un segundo
factor en el host. Lo acotan la vigencia corta, los límites y que el código
sólo aparece en la terminal del instalador. Fuera del MVP: ver en la terminal
qué navegador usó el código y el botón «no fui yo».

Validación: pruebas Go con login correcto e incorrecto, mensaje idéntico para
usuario inexistente, cuenta deshabilitada, sesión revocada, expiración
absoluta e inactividad, Origin y CSRF ausentes, límite de intentos, contraseña
temporal, cambio de contraseña, administración y su alcance, ausencia de la
contraseña en respuestas, auditoría y salida, y rechazo cruzado de aserciones
Ozal y cookies locales entre modos. Una prueba de integración crea el admin por
CLI con la contraseña por entrada estándar y recorre login, cookie, CSRF,
temporales, revocación, cambio de contraseña y logout por HTTP real; las
pruebas unitarias de la web cubren el encabezado CSRF y la API simulada de la
demo. `verify:go:local` repite el recorrido en Chromium contra Go y SQLite:
error genérico, login, creación de un lector, cambio de contraseña temporal y
deshabilitado con revocación de la sesión viva. Falta la aceptación desplegada
con TLS/proxy.

## QA sin login real

En el producto nuevo, las pruebas automatizadas pueden inyectar un proveedor
de identidad falso desde el harness. El laboratorio sintético cubre QA
manual con datos ficticios en loopback. No hay cuenta de emergencia
productiva ni credenciales predeterminadas.
