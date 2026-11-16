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
La integración Ozal se verifica con claves efímeras y un gateway de prueba;
la aceptación contra una instancia real de Ozal, la aceptación desplegada del
modo local con TLS/proxy y los despliegues siguen pendientes. OIDC genérico
todavía es un diseño, no otro modo disponible.

## Modos separados

| Contexto | Entrada | Autoridad de Custodia |
|---|---|---|
| Demo pública | Sin login, datos sintéticos. | Ningún acceso a inventario privado. |
| `serve --auth-mode local` | Cookie de sesión emitida tras usuario y contraseña. | Cuentas locales habilitadas, permisos por conexión y auditoría. |
| `serve --auth-mode ozal` | Aserción firmada nueva para cada petición, también assets. | Principales habilitados, permisos por conexión y auditoría. |
| `serve-lab` | Bearer aleatorio de laboratorio, sólo loopback. | Principales sintéticos y concesiones explícitas. |

Todos los servidores escuchan sólo en IP numérica loopback. Eso no autentica
al usuario. `--auth-mode` es obligatorio en serve y admite exactamente
`local` u `ozal`; no hay modo híbrido ni valor por defecto. En modo `ozal`
se exige la firma incluso con acceso directo y no se admite token de
laboratorio, cookie de sesión local, headers de usuario/email ni login local.
En modo `local` no se registra el receptor Ozal: una aserción, un bearer o
un header de usuario no autentican; sólo la cookie de sesión. Cuando falla
un modo, la petición se rechaza; no se prueba el otro. La instalación debe
cerrar también los caminos directos al backend y proteger su host.

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

## Qué sería «OIDC propio» en Custodia

Custodia sería **cliente OIDC**, también llamado relying party. El proveedor
gestiona contraseña/passkey/MFA y entrega la identidad; Custodia no crea un
servidor de identidad. La configuración usa emisor, client ID, referencia al
secreto y callback registrados; ningún nombre de fabricante forma parte del
dominio del producto.

Flujo objetivo: navegador → proveedor → callback de Custodia. Authorization
Code con PKCE S256; `state` y `nonce` de un solo uso y con vencimiento.
El backend canjea el código, valida firma mediante claves del emisor
configurado, `iss`, `aud`, `exp`, `nonce` y `azp` cuando corresponde. No
autentica con un access token sin validar su propósito ni acepta un issuer
elegido libremente por el navegador.

La identidad persistida es emisor + `sub`, nunca sólo email. Después de
autenticar, Custodia crea una sesión opaca revocable en servidor: cookie
Secure/HttpOnly/SameSite compatible con el callback, rotación, expiración y
CSRF en mutaciones. Tokens y secretos permanecen en backend. Validar destinos
de retorno y distinguir logout local de cierre de sesión del proveedor.
No enlazar automáticamente cuentas de OIDC y Ozal por email.

Validar con un proveedor simulado: código repetido, state/nonce ausentes,
firma/emisor/audiencia incorrectos, expiración, rotación de claves, usuario
deshabilitado, revocación local y permisos insuficientes. Un proveedor real
se acepta después con su configuración de laboratorio. Posterior al camino inicial con Ozal; ver el tracker.

Referencias: [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html)
y [OAuth Security Best Current Practice](https://www.rfc-editor.org/rfc/rfc9700.html).
Seleccionar una librería Go mantenida al implementar; no escribir verificación
criptográfica ni un protocolo OAuth a mano.

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
su SHA-256, el principal, creación, última actividad y vencimiento. Cookie
`__Host-custodia_session` (o `custodia_session` con origen HTTP loopback) con
`HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`, sin dominio ni Max-Age.
Expiración absoluta 12 horas e inactividad 30 minutos, verificadas en el
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
consultan dos límites persistidos en SQLite: 5 fallos por usuario o 50 por
IP del socket en 15 minutos bloquean esa clave 15 minutos con 429; el
bloqueo aplica también a usuarios inexistentes, por lo que no los revela.
Detrás de un proxy en loopback la IP es la del proxy y su límite actúa como
freno global. Un login exitoso limpia el contador del usuario. Si la tabla de
intentos llega a 65.536 claves vigentes, el login falla cerrado con 429.

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
