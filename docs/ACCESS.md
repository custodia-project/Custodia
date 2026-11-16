---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Acceso: Ozal, autorización y pruebas

El backend Go implementa un receptor de identidad firmada de Ozal por
petición. El alta inicial del administrador es un comando explícito en el
host. La integración se verifica con claves efímeras y un gateway de prueba;
la aceptación contra una instancia real de Ozal y su despliegue siguen pendientes.
OIDC genérico todavía es un diseño, no otro modo disponible.

## Modos separados

| Contexto | Entrada | Autoridad de Custodia |
|---|---|---|
| Demo pública | Sin login, datos sintéticos. | Ningún acceso a inventario privado. |
| `serve` detrás de Ozal | Aserción firmada nueva para cada petición, también assets. | Principales habilitados, permisos por conexión y auditoría. |
| `serve-lab` | Bearer aleatorio de laboratorio, sólo loopback. | Principales sintéticos y concesiones explícitas. |

Ambos servidores escuchan sólo en IP numérica loopback. Eso no autentica al
usuario: serve exige la firma incluso con acceso directo. No admite token de
laboratorio, cookie de aplicación, headers de usuario/email ni login local.
No hay fallback cuando falla Ozal. La instalación debe cerrar también los
caminos directos al backend y proteger su host.

## Contrato firmado

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

## Primer administrador y lectores

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

## Cierre de sesión

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

## Login local de QA

La versión anterior tiene un endpoint local condicionado por su modo de
autenticación. No se probó la configuración que está corriendo. Ocultar el
botón no establece que el endpoint esté deshabilitado.

En el producto nuevo, las pruebas automatizadas pueden inyectar un proveedor
de identidad falso desde el harness. Para QA manual, si hace falta un login
local, usar un modo `local-dev` explícito, datos ficticios y escucha loopback;
rechazar su arranque fuera del entorno de desarrollo. En los modos reales no
se registra ese endpoint y no se distribuyen credenciales predeterminadas.
Probar peticiones directas al endpoint y el rechazo de configuración inválida,
además de la UI. Este diseño no añade una cuenta de emergencia productiva.
