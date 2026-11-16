---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Entregas y evidencia

## Acceso OIDC configurable desde la consola — 16/11

`PROD-OIDC-001`: `serve --auth-mode oidc` es un modo independiente, sin
fallback a contraseña ni modo híbrido. La migración aditiva 10 agrega
`oidc_config`, `oidc_states`, `oidc_accounts` y `oidc_pending`; las sesiones
siguen usando `local_sessions` (token opaco, sólo SHA-256 en SQLite) con el
mismo contrato de 12 horas/30 minutos, CSRF y auditoría.

La consola (`Usuarios y permisos → Acceso OIDC`) guarda emisor, client ID,
referencia del secreto, URL de retorno, scopes y claims. Guardar deja un
borrador inactivo y revoca las sesiones OIDC; activar exige una prueba
correcta del descubrimiento y del JWKS de la configuración vigente hecha en
los últimos diez minutos. El cliente usa Authorization Code + PKCE S256,
`state` de un solo uso con hash en SQLite y cookie `Lax` ligada al callback,
`nonce`, y verificación de ID token con `go-oidc/v3` (emisor, audiencia,
expiración, nonce, `azp`). El access token no autentica.

La identidad es emisor + sujeto. Una identidad válida desconocida queda en
una cola acotada (1.024) sin principal ni concesión; un administrador la
aprueba indicando un subconjunto de sus propias conexiones, en una transacción
con su auditoría. El email es sólo presentación. El administrador local
conserva un formulario de contraseña explícito de emergencia, con el mismo
throttling y errores genéricos; una cuenta local que no sea administradora no
puede usarlo. El secreto se lee de `env:`, `file:` o `secret:`
(`CUSTODIA_OIDC_SECRET_DIR`) y nunca se guarda, devuelve, audita ni registra.

Pruebas: proveedor simulado RS256 con descubrimiento, JWKS y endpoint de
token; guardar/probar/activar/desactivar; activación sin prueba, con prueba
fallida o vieja; state ausente, repetido, vencido o con cookie distinta;
nonce y audiencia incorrectos; cola y aprobación con conexiones fuera del
alcance; sesión, logout, deshabilitado con revocación y cambio de
configuración con revocación; administrador de emergencia. La web agrega el
botón del proveedor, el formulario de emergencia, la consola y una prueba e2e
con la API simulada. `verify:go:oidc` repite en Chromium, contra Go y SQLite
con un proveedor falso local, la consola (borrador, prueba, activación), la
aprobación con alcance y el login con sesión OIDC. La prueba de configuración no completa un login
interactivo; falta aceptar un proveedor real y desplegar con TLS/proxy.

## Integración de login local e inventario — 16/11

Las dos entregas conviven en el mismo árbol. La allowlist de preferencias de
workspace en Go dejó de aceptar sólo `person`: valida los IDs del catálogo de
la UI, acepta `inventory` y rechaza IDs desconocidos sin reflejarlos; la
pantalla de Inventario ya guarda accesos rápidos. Se agregó
`web/tests/e2e/cross/inventory.spec.ts` (mock, navegador): métrica no
disponible distinta de cero, cero confirmado y drill-down con los registros
exactos. El command center desambigua el «Inventario» de Netskope de
«Inventario» de herramientas por grupo, sin cambiar la intención de la
prueba.

## Inventario de herramientas y métricas declaradas por conector — 16/11

`PROD-METRIC-001`: el binario declara las métricas junto al catálogo de
conectores y las calcula al leer sobre los datos ya publicados, sin tablas
nuevas. `server/internal/metrics` define el modelo (ID versionado, nombre,
explicación, colección, cálculo y unidad) con cinco cálculos: conteo, conteo
con filtro, proporción, antigüedad con umbral fijo y conteo sin referencia en
una colección hermana. La validación de pruebas exige que cada colección
pertenezca al descriptor del conector y que cada campo usado exista en la
allowlist normalizada de `server/internal/inventory`; un conector planificado
no puede declarar métricas.

CyberArk PAM declara ocho métricas: Safes totales, Safes sin miembros, cuentas
totales, cuentas con gestión automática deshabilitada, su proporción, cuentas
con último cambio fallido, plataformas inactivas y vínculos
plataforma→PSM `missing`/`ambiguous`. «Safes sin miembros» usa el cruce ya
publicado entre `safes` y `safe_members`; la resolución PSM sale de
`platform_psm_links`, no se infiere al leer. Cada valor se calcula sobre la
última corrida `completed` o `partial` de la colección, restringido a su
alcance y generación: un cambio de alcance no suma registros de la línea base
anterior.

Reglas verificadas: una colección no configurada o sin publicación devuelve
`unavailable` con valor nulo, nunca cero; un cero confirmado es un valor real;
una corrida parcial conserva el conteo y lo marca `partial`; una proporción o
un `count_without` con denominador/referencia parcial no se calculan; una
corrida fallida no reemplaza la fecha ni los datos de la última publicación.
La API `GET /api/inventory/tools` y
`GET /api/inventory/metrics/{metric_id}/records?connection=...&limit=N` filtra
por concesiones antes de calcular, no distingue una conexión oculta de una
inexistente (404) y el detalle devuelve exactamente los registros contados,
con `total` completo y `truncated` cuando aplica.

La pantalla Inventario usa el sistema visual existente: tarjeta por conexión,
colecciones con estado y fecha, completitud, métricas con valor/nombre/
explicación/fecha y detalle de registros. La navegación la agrega al área
Conexiones y a Command-K; también queda enlazada desde Primeros pasos. Respeta
los permisos porque sólo recibe del backend las conexiones legibles.

Evidencia: `TestDeclaredMetricsAreValid`,
`TestValidateRejectsUndeclaredFieldsAndUnversionedIDs`,
`TestEvaluateNeverInventsZero`, `TestEvaluateKinds`,
`TestInventoryMetricsOverPAMFixture`, `TestInventoryMetricsUseTheLastPublicationScope`,
`TestInventoryReportsAnUnfinishedLastRun`, `TestInventoryFirstPartialPublicationIsMarked`,
`TestInventoryConfirmedZeroIsARealZero` y
`TestInventoryHidesConnectionsWithoutReadGrant`, con el fixture PAM sintético.
`go vet ./...` y `go test -race ./...` pasan. En web pasan `typecheck`, `lint`,
`verify:styles`, `test:unit` (incluye ocho pruebas nuevas de presentación) y el
mock de la demo queda pendiente de integrar. No hay aceptación contra un tenant
CyberArk real, no se declaran métricas para otros conectores y la demo estática
todavía no sirve estas rutas.

## Login local, sesiones y administración mínima — 16/11

[ACCESS.md](ACCESS.md#modo-local-contrato) fija el contrato antes del código:
argon2id v19 con 64 MiB, 3 iteraciones, paralelismo 2, sal de 16 bytes y hash
de 32; sesión opaca de 12 h absolutas y 30 min de inactividad con cookie
`HttpOnly`/`Secure`/`SameSite=Strict`; CSRF de sesión; límites de 5 fallos por
usuario y 50 por IP en 15 min; modo `local` u `ozal` explícito, sin fallback.

La migración aditiva 9 crea cuentas locales, sesiones y bloqueos persistidos
sin duplicar los principales ni las concesiones por conexión existentes; se
re-aplica con `IF NOT EXISTS` y conserva datos al actualizar desde la versión
8. `serve --auth-mode local --public-origin` monta el handler local; `create-admin`
y `reset-password` toman la contraseña por la primera línea de un archivo
privado o de la entrada estándar, nunca por argumento, y auditan como `host`.
La API `login/logout/password/status` y la administración `/api/access/users`
(sólo administrador, CSRF y auditoría) no devuelven contraseñas ni hashes.

`go vet ./...`, `go test -race ./...` y `go build ./...` verdes en `server/`;
la prueba de integración crea el administrador por CLI con la contraseña por
stdin y recorre por HTTP real login, cookie, CSRF, contraseña temporal,
deshabilitado, revocación, cambio de contraseña y logout. Del lado web,
`typecheck`, `lint`, `test:unit` (497) y `build:demo` verdes; la API simulada
de la demo incorpora login local y cuentas con sesiones revocables, y una
prueba de navegador recorre login, creación y deshabilitado contra esa API.
`verify:go:local` repite en Chromium contra Go/SQLite el error genérico de
login, la creación de un lector, el cambio de contraseña temporal y el
deshabilitado que revoca su sesión viva. Faltan TLS/proxy y aceptación
desplegada.

## Una conexión de tenant, varias colecciones — 16/11

La migración aditiva 8 agrega `group_id` a `lab_sync_sources` y lo rellena con
el `connection_id` existente, sin tocar las migraciones 1–7. El alta acepta
`collections[]`: crea una fila de fuente por colección bajo el mismo grupo, con
la misma URL, credencial, CA y modo de auth, y concede lectura/`can_run` al
creador en cada una. Un tenant CyberArk se conecta una sola vez para Safes,
miembros, cuentas, plataformas y catálogos PSM, en lugar de repetir el alta.

El probe de grupo une los endpoints de sus colecciones, exige que todas
compartan configuración y audita una sola vez. Cada colección mantiene su
corrida, línea base, agenda, retiros y permisos: una corrida parcial de una
colección no afecta a las demás. La UI muestra las colecciones como
checkboxes, con “Todas” y “Ninguna”, y el aviso usa el `group_id`.

Verificación: `TestTenantConnectionCollectsSeveralCollections` registra Safes y
cuentas, comprueba dos filas con el mismo `group_id`, un probe de grupo con
ambos endpoints y una corrida completada por colección con sus registros.
`verify:go:gateway` repite el recorrido desde la UI con un tenant TLS: elige
CyberArk, marca `accounts`, conecta, prueba los dos endpoints y recolecta dos
Safes y dos cuentas.

## Alta de conexiones por herramienta y probe desde la instalación vacía — 16/11

Custodia ya no depende de `add-http-lab` para incorporar una fuente. El registro
`server/internal/connectors` declara qué herramientas están implementadas
(CyberArk PAM; inventario HTTP sintético) y cuáles son sólo hoja de ruta
(Netskope, QRadar, Palo Alto, FortiGate, CrowdStrike, Tenable). Un conector
planificado no declara endpoints, colecciones ni credenciales, y el alta lo
rechaza: no se inventan contratos por herramienta.

La migración aditiva 7 agrega a `lab_sync_sources` el modo de auth y las
referencias `env:`/`file:`/`secret:` (bearer, OAuth client-credentials, mTLS),
la CA y el par cliente. Las migraciones 1–6 no se tocaron; las conexiones
existentes quedan con `auth_mode=none` y el mismo comportamiento de fixture.

API administrativa:

- `GET /api/ingestion/connectors`: catálogo de descriptores.
- `POST /api/ingestion/connections`: alta sin contactar el tenant; exige
  administrador, HTTPS con path opcional, par conector/colección válido y
  referencias de credencial válidas; audita `connection_register` y concede
  lectura/`can_run` al creador. La respuesta no repite las referencias.
- `POST /api/ingestion/connections/{id}/probe`: ejecuta el mismo plan del probe
  sobre los endpoints de esa colección, devuelve el reporte saneado y audita
  `connection_probe`. No publica observaciones ni encola corridas.

El adaptador CyberArk acepta ahora un tenant HTTPS con prefijo de path y un
cliente autenticado provisto por `probe.Prepare`; el fixture loopback conserva
su ruta sin credenciales. Una conexión registrada se recolecta con el worker
normal: el probe, la corrida y el adaptador comparten origen, CA, auth y
límites.

La UI de Actividad suma la pestaña **Conexiones**: elige herramienta, valida en
el navegador las mismas reglas del backend (HTTPS, referencias, colección,
intervalo), registra el tenant, prueba el acceso y muestra el resultado por
endpoint. Sólo se ofrece a `installation:manage`. El backend ahora anuncia
`audit:read` al administrador para que la pantalla de Actividad sea alcanzable
con la identidad real, no sólo en el laboratorio.

Verificación: `TestRegistrySeparatesImplementedFromPlanned`,
`TestTenantOnboardingProbeAndCollection` (alta → probe → corrida → registros
sobre TLS), `TestTenantOnboardingRejectsUnsafeConfiguration`,
`TestTenantProbeFailsWhenCredentialSourceIsAbsent`,
`TestRemoteTenantHTTPSWithPathPrefix` y
`TestRemoteTenantRejectsUnsafeBase`, entre otras. `verify:go:gateway` recorre
el alta completa desde la UI contra un tenant TLS: elegir CyberArk, pegar la
URL, referencia `env:`, CA local, conectar, probar y recolectar dos Safes.
Límites: no se contactó un tenant real; Netskope y las demás herramientas
siguen sin contrato Go.

## Backup/restore SQLite y paquete autohospedado — 16/11

Se agregó `server/internal/sqlitebackup` y los comandos `backup`/`restore`.
El backup usa `VACUUM INTO` sobre una conexión separada, de modo que no
modifica la base fuente y puede ejecutarse con el servicio en marcha. El
restore valida integridad y claves foráneas, copia a un temporal en el
directorio destino, lo sincroniza, lo enlaza sin sobrescribir y vuelve a
verificar integridad y esquema. El directorio destino debe existir y no tener
`state.db`; el token de laboratorio, la configuración de gateway y los
secretos se provisionan fuera del backup.

`scripts/package-go.mjs` construye un paquete con binario Go,
`web/` compilado, READMEs, `LICENSE`, documentación y `manifest.json` con
SHA-256 por archivo, además de un `.tar.gz`. No incluye bases, tokens,
fixtures, `.env` ni configuración local. `npm run verify:go:ops` construye y
extrae el paquete en un host descartable, verifica el manifiesto, instala,
ejecuta la migración dos veces, hace backup con el servicio en marcha,
restaura en otra instancia, reinicia y autentica contra la instancia
restaurada y comprueba la web/API empaquetadas. No toca servicios activos ni
publica nada.

Verificación: `go test -race ./internal/sqlitebackup ./cmd/custodia` y
`npm run verify:go:ops`. Límites: sin aceptación real, TLS/secretos de
producción, upgrade ni despliegue.

## Probe TLS de lectura contra fixture — 16/11

Se implementó el transporte del probe autorizado en
`server/internal/probe` y el comando `custodia probe-pam`, sin contactar
ningún tenant. `RunPlan` soporta bearer, OAuth client-credentials y mTLS.
Resuelve referencias `env:`/`file:` y `secret:` desde
`CUSTODIA_PROBE_SECRET_DIR` fuera del repositorio, usa TLS verificado con
mínimo TLS 1.2 y CA adicional opcional, rechaza redirects, limita cuerpo a
1 MiB y timeout por endpoint, y devuelve un reporte saneado por endpoint con
estado, duración, cantidad de objetos y código estable. El CLI emite ese JSON y
distingue códigos de salida `0` (todo `200`), `1` (fallo de endpoint con
reporte) y `2` (plan/credencial/autenticación/CA inválidos). La aceptación
contra un tenant y el contrato real de scopes siguen pendientes.

Verificación: `go test -race ./internal/probe` usa
`httptest.NewTLSServer` y un servidor mTLS con CA temporal. Cubre
autorización explícita, URLs HTTPS sin usuario/query/path, endpoints
allowlisted, timeout, resolución `env:`/`file:`/`secret:`, OAuth
client-credentials, mTLS, redirect rechazado, 401/403/429, cuerpo excesivo,
JSON inválido, content type incorrecto, CA no confiable y ausencia de
token/cuerpo/clave en el JSON saneado. `go test ./cmd/custodia` prueba el CLI
con bearer `env:`/`file:`, OAuth con `secret:`, mTLS y los códigos de salida
`0`, `1` y `2`; una prueba de binario compilado verifica el código de proceso
real.

## Adaptador CyberArk PAM Safes, miembros, cuentas, plataformas, catálogos PSM y vínculos contra fixture — 16/11

Se agregó el segundo conector de lectura, `cyberark-pam-safes-v1`, con las
formas documentadas de CyberArk `/API/Safes`,
`/API/Safes/{safeName}/Members`, `/API/Accounts`, `/API/Platforms`,
`/API/PSM/Servers` y `/API/PSM/Connectors` extraídas de DS Hub. El adaptador
`server/internal/adapters/cyberarkpam` lee `value[]`/`count`/`nextLink` o
catálogos `[...]`/`Total`, limita página, cuerpo, timeout y cursor, no sigue
redirecciones ni envía credenciales/cookies, y sólo acepta un origen HTTP
numérico de loopback en este tramo. El `nextLink` se restringe al mismo origen
y al path esperado; un destino externo, otro path, fragmento o usuario falla
con `foreign_cursor`. La colección normalizada `safes` tiene allowlist de
metadatos y valida número de Safe positivo, URLs, booleanos, retenciones y
epochs RFC3339. Se descartan secretos y cualquier campo fuera de la allowlist.

La colección `safe_members` enumera `/API/Safes` completo y luego pagina los
miembros de cada Safe. El cursor opaco lleva el índice de Safe y el `nextLink`
del proveedor; un cursor que cambie de Safe u origen se rechaza. La allowlist
es `safeNumber`, `safeName`, `memberId`, `memberName`, `memberType`,
`membershipExpirationDate`, `isExpiredMembershipEnable`, `isPredefinedUser`,
`isReadOnly` y `permissions`; el ID externo es `safeNumber:memberId` y los
permisos se guardan como objeto JSON acotado. El adaptador valida el total de
miembros por Safe al cerrar su lista y el total agregado al cerrar la
colección: una lista corta o un total cambiante falla sin publicar. Un Safe
sin miembros sólo acredita cero con `value:[]` y `count:0` explícitos.

La colección `accounts` pagina `/API/Accounts` y resuelve el Safe padre por
nombre contra la enumeración acotada. La allowlist es `account_id`,
`safe_number`/`safe_name`, `account_name`, `user_name`, `address`,
`platform_id`, `account_type`, `category`, `automatic_management_enabled`,
`management_status` y `secret_modified_at`; el ID externo es
`safeNumber:accountId`. El subconjunto `secretManagement` se reduce a esos
tres campos y se excluyen contraseñas, `platformAccountProperties`,
`manualManagementReason`, `lastModifiedBy`, `secretType` y el payload. Una
cuenta sin Safe visible o sin ID se omite y produce el aviso
`skipped_accounts`; el runner termina `partial`, publica lo leído y no retira
cuentas ni reemplaza la línea base. Una lista completa con total inconsistente
falla sin publicar.

La colección `platforms` lee `/API/Platforms`, que el contrato verificado
entrega `{Platforms:[...], Total:N}` en una sola respuesta. El adaptador
valida `Total` contra la lista y sirve páginas locales acotadas con cursor
propio, sin inventar parámetros de paginación del proveedor. La allowlist es
`general.id` (ID externo), `general.name` (o el ID como fallback),
`general.active`, `general.platformBaseID`, `general.platformType`,
`general.systemType` y `sessionManagement.PSMServerID`. Se excluyen
`properties`, políticas, reglas de contraseña y el payload. El detalle
`/API/Platforms/{id}` y `Details.AllowedSafes` quedan fuera de este tramo;
`psm_server_id` es una referencia textual sin arista PSM.

Los catálogos `psm_servers` y `psm_connectors` leen
`/API/PSM/Servers?limit=1000` y `/API/PSM/Connectors?limit=1000`, validan
`Total` contra la lista y sirven páginas locales acotadas. La allowlist de
servidor es `ID` (externo), `Name` (fallback al ID) y `Address`; la de conector
es `ID` (externo) y `DisplayName` (fallback al ID). Se excluyen propiedades,
capacidad, salud y el payload. Los catálogos no crean relaciones: el cruce de
`psm_server_id` contra `psm_servers` queda pendiente.

`platform_psm_links` cruza en una corrida la referencia
`sessionManagement.PSMServerID` de cada plataforma contra `psm_servers`.
`exact` coincide literalmente; `casefold` resuelve un único ID que difiere
sólo en mayúsculas; `ambiguous` detecta dos o más IDs distintos que difieren
sólo en mayúsculas y publica la fila sin objetivo; `missing` conserva la
referencia cruda y la marca. El ID externo es `platformID:referencePSMID`; la
allowlist es `platform_id`, `platform_name`, `reference_psm_id`,
`psm_server_id`, `psm_server_name` y `resolution`. Una fila ambigua no es una
arista, es evidencia de la omisión; una faltante no inventa un servidor. Sólo
una corrida completa retira vínculos cuya referencia desapareció; parcial y
fallida no retiran. El cruce no usa nombres de plataforma, prefijos ni
similitud.

El runner acepta `Page.Expected` y falla la corrida si el total reportado por
el proveedor no coincide con lo leído o cambia entre páginas; esa comprobación
cubre Safes, miembros, cuentas, plataformas y catálogos PSM. También acumula
avisos de página y convierte la corrida en `partial` para que un registro
omitido nunca autorice retiros. La configuración exige el par
conector/colección (`cyberark-pam-safes-v1` con `safes`, `safe_members`,
`accounts`, `platforms`, `psm_servers` o `psm_connectors`;
`http-inventory-v1` sólo con `users`/`devices`) y el CLI infiere la generación
del conector cuando se omite. `source_system` se deriva del conector:
`cyberark` para PAM, `netskope` para el laboratorio normalizado; las etapas
por fuente se filtran por ese valor.

Verificación: `npm run verify:go` incluye pruebas del adaptador (paginación de
Safes, miembros, cuentas, plataformas, catálogos PSM y vínculos; total
incompleto/cambiante; nextLink externo/repetido/cruzado; 401/403/429/5xx;
allowlist/epochs/permisos; `secretManagement`; cuentas omitidas; resoluciones
exact/casefold/missing/ambiguous; cero confirmado; redirects; content type) y
pruebas end-to-end en `lab` con worker, publicación, procedencia, parcial sin
retiro, retiro sólo tras corrida completa, aislamiento de historial y permisos.
`npm run verify:go:web` levanta ocho conexiones fixture (usuarios, Safes PAM,
miembros, cuentas, plataformas, servidores PSM, conectores PSM y vínculos
plataforma↔PSM), ejecuta sync desde la UI y por API, comprueba 2 páginas/3
registros en Safes, 4 páginas/4 miembros, 2 páginas/3 cuentas, 2 páginas/3
plataformas, 2 páginas/3 servidores PSM, 2 páginas/2 conectores PSM, 2
páginas/3 vínculos, el aislamiento `scope=cyberark`, la allowlist sin secretos
y la agenda.

Límites: no hay credenciales, TLS, probe, tenant real ni aceptación; el
detalle de plataforma y `AllowedSafes` no se leen todavía. La cadena completa
`users → roles → safes → platforms → psm` y el probe/TLS autorizado quedan
pendientes. El adaptador usa los contratos CyberArk, pero el tramo es un
fixture loopback y no acredita soporte del fabricante ni cierra
`PROD-REAL-001`.

## Worker, agenda y UI de sincronización — 16/11

La migración aditiva 6 agrega `interval_minutes` por conexión sync sin tocar
las migraciones 1–5. `serve` y `serve-lab` inician un worker en segundo plano:
reclama la corrida `queued` más antigua, mantiene heartbeat y lease cada 30 s,
reencola leases vencidos y procesa de a una corrida para conservar el orden de
escritura de SQLite. La API ya no ejecuta la extracción dentro del POST: el
pedido devuelve 202 con la corrida durable y la UI la consulta.

El scheduler usa el intervalo por conexión (`0` deshabilita; `5–525600`
minutos). Cuando no hay corrida activa y pasó el intervalo desde el último
intento, encola con actor `system:scheduler`; un fallo se reintenta en el
siguiente intervalo. No hay todavía backoff exponencial, prioridad por fuente
ni pool de workers. `PUT /api/ingestion/schedules/{connection}` persiste y
audita `sync_schedule`; `GET /api/ingestion/schedules` devuelve intervalo,
frescura, intervalos desde el último éxito y próximo intento.

Se completaron los contratos que consume la UI: `POST /api/ingestion/all/runs`,
`POST` y `PUT` de agenda por conexión, `GET
/api/ingestion/sources/{source}/stages/latest` y `/history`. `auth/status`
anuncia `ingestion:run` cuando el principal tiene lectura y `can_run` en al
menos una conexión; la vista de agenda acepta esa capacidad para editar el
intervalo y el menú de usuario inicia una sincronización real. La UI conserva
los estados `queued/running/completed/partial/failed`, la evidencia por
registro y la agenda.

Verificación: `npm run verify:go` pasó con vet, race y build; incluye worker,
scheduler, fencing, agenda, `/all/runs`, etapas y permisos.
`npm run verify:go:web` pasó con UI real, Go, SQLite y un servidor HTTP local
sintético: inició sync desde el menú, esperó la corrida, comprobó 2 páginas/3
registros, consultó procedencia, encoló `/all/runs`, guardó el intervalo y
verificó `ingestion:run`. `verify:go:gateway` conservó la regresión de Ozal.
`npm run verify` pasó con documentos y 477 unitarias web.

Límites: worker único sin backoff/pool, agenda por conexión, sin proveedor
real, sin revisión humana por HTTP, sin administración de conexiones/probe y
sin despliegue. El orden de dos corridas del mismo segundo no es significativo.

## Sincronización HTTP de laboratorio — 16/11

Primer conector de lectura acotado sobre el laboratorio Go; no cierra
PROD-CONN-001 ni PROD-INV-001. El contrato normalizado (`internal/inventory`,
sin SQL ni HTTP) define objetos con ID externo opaco de hasta 512 bytes UTF-8
sin caracteres de control y atributos con allowlist por colección: users
requiere email acotado y admite name; devices requiere user_id y admite
last_signal_at RFC3339. Los valores se recortan y limitan a 1024 bytes UTF-8;
los atributos no permitidos se rechazan con códigos de error estables.
`Deduplicate` descarta observaciones idénticas y rechaza las contradictorias
antes de persistir, como la frontera de normalización Python.

El adaptador `internal/adapters/httpinventory` sólo acepta orígenes http://
numéricos de loopback, sin redirecciones, cookies, credenciales, query ni
fragmento; cuerpo acotado a 1 MiB, página acotada al límite pedido, cursor
opaco de hasta 1024 bytes, decodificación JSON estricta sin contenido final y
`objects`/`next_cursor` obligatorios: un cuerpo vacío o nulo no acredita cero.
El runner `internal/sync` acota la corrida (página de hasta 500, máximo de
páginas y registros): un error de integridad descarta la corrida completa;
un error de transporte o cancelación después de páginas deja sólo lo
recolectado como corrida parcial, con avisos max_pages/max_records y sin
inferir retiros. Un cursor repetido se rechaza.

La migración 5 es aditiva y conserva las anteriores: fuentes sync con un
conector registrado, concesiones can_run por principal, corridas durables con
clave de idempotencia por solicitante y conexión, una sola corrida activa por
conexión, líneas base por conexión/colección y revisiones humanas en tabla
separada; lab_observations suma estado y procedencia (first/last run, scope,
generación, retiro). Cada corrida guarda la URL base del enqueue. En esa
entrega el único conector era `http-inventory-v1`; el tramo de Safes PAM sumó
`cyberark-pam-safes-v1` sin cambiar la migración. La publicación es transaccional y auditada en cada transición:
una corrida completed con igual scope y generación que la línea base puede dar
de baja ausencias; una parcial actualiza observaciones pero nunca retira ni
reemplaza la línea base; una fallida no publica observaciones ni reemplaza la
línea base, aunque registra su estado y `sync_complete`; un conjunto
contradictorio se publica como fallida conservando la línea base anterior.
Antes de publicar se revalida que la fuente siga habilitada con la misma URL,
scope y generación, y que el solicitante conserve lectura y can_run; si no,
falla con `configuration_changed` o `authorization_revoked` sin escribir.
El fencing usa worker, lease y `attempt_count`: un intento viejo no puede
renovar el lease ni publicar después de un requeue o de un nuevo claim. El
vencimiento se reencola de forma explícita, acotada a la conexión y auditada
con el actor que la solicita; un fallo de heartbeat terminaliza la corrida si
el intento aún es dueño, o deja que el intento nuevo la retome si perdió el
lease. La publicación terminal sobrevive a la cancelación del pedido que
disparó la corrida.

La API de laboratorio expone lectura de corridas, corrida por ID con etapas,
agendas con último éxito y fallos consecutivos, registros con estado y
procedencia, y POST de corrida por conexión con cuerpo vacío, Idempotency-Key
acotada, 409 si ya hay una corrida running con lease vigente, 403 sin can_run
y 404 que no revela conexiones ocultas. Una corrida queued o con lease vencido
se reencola y retoma en ese mismo POST. Las lecturas filtran por concesión en
la consulta; los registros se leen en una transacción de sólo lectura y las
corridas ocultas no se distinguen de inexistentes.
add-http-lab registra la fuente desde el host con validación de loopback,
colección y scope, concesiones sobre principales habilitados existentes y
auditoría atómica. Las conexiones HTTP sintéticas no alteran las capacidades
Identity/Netskope de Personas/cobertura, que ahora excluyen observaciones
retired. Las revisiones humanas tienen tabla y métodos probados pero todavía
sin endpoint HTTP.

Verificación: verify:go pasó con vet, race y build, incluyendo los paquetes
nuevos inventory (contrato y deduplicación), httpinventory y sync (límites,
parciales, cursores repetidos y errores de integridad), pruebas de store
(migración aditiva idempotente, configuración rechazada, publicación y
retiros comparables, parcial sin bajas, fallo atómico, idempotencia por
conexión, una corrida activa por conexión, fencing por worker/intento tras
lease vencido, revalidación de fuente y permisos antes de publicar,
aislamiento de dos conexiones, revisiones que sobreviven al refresco, esquema
que rechaza versiones futuras) y la API HTTP de sync. npm run verify pasó. En
esa entrega el gate de navegador no ejercitaba ingesta; la entrega de
worker/agenda agrega esa cobertura. No se repitió la suite amplia e2e de web:
sus ocho residuales siguen registrados en PROD-VERIFY-001 y no se presentan
como regresiones nuevas ni como suite verde.
No hay conector real ni
proveedor, agenda programada, API administrativa de concesiones ni endpoint
de revisiones; el source_system reportado no acredita Netskope. No se
acredita una instancia real de Ozal ni despliegue. Sin commit, push,
publicación ni servicios modificados.

**Cierre del tramo (16/11):** adaptador HTTP sintético, persistencia,
publicación, autorización y pruebas de laboratorio cerrados. El worker, la
agenda y la UI se completaron en la entrega «Worker, agenda y UI» de arriba;
siguen fuera los retries con backoff/pool, el adaptador de un proveedor real,
la administración de conexiones, la aceptación contra Ozal real y el
despliegue.

## Receptor Ozal y administrador inicial — 16/11

serve verifica una aserción nueva X-Ozal-Assertion por petición, incluidos los
assets: EdDSA/Ed25519 mediante go-jose/v4 v4.1.9, claves públicas fijadas,
emisor/audiencia/sujeto/tiempo y jti único. Rechaza headers falsos/duplicados,
claves dinámicas, replay y credenciales del laboratorio. No hay sesión propia
ni fallback local. Las mutaciones exigen Origin configurado.

init crea una base privada vacía, sin seed ni credencial. La migración aditiva
4 conserva las anteriores y agrega administración/bootstrap y replay SQLite.
bootstrap-admin vincula emisor/sub una sola vez desde el host; no usa correo
o grupos como autoridad. POST/DELETE de lectores aplica alcance por conexión,
rechaza escalamiento y audita atómicamente. La UI identifica mode ozal y cierra
la sesión mediante POST del navegador al endpoint de Ozal del mismo origen.
Todavía no hay pantalla de lectores ni recuperación completa del administrador.

verify:go pasó con vet, race y build; incluye validaciones negativas de firma,
claims, origen, rotación, replay concurrente/persistido/saturado, bootstrap único
y rollback por auditoría. verify:go:gateway pasó con frontend real, Go y SQLite:
dos sujetos con mismo correo/grupos, desconocido denegado, alta explícita de
lector Identity, cobertura denegada, replay conservado al reiniciar, revocación,
ausencia de fallback y logout en navegador. La prueba de gateway usa claves
nuevas y selección sintética de identidad; no acredita una instancia real de
Ozal, passkeys, políticas o despliegue. verify:go:web también pasó como
regresión del laboratorio; npm run verify pasó con documentos, 18 casos del
modelo y 477 unitarias web. Typecheck, lint y estilos pasaron. La suite amplia
anterior conserva sus ocho residuales registrados; no se repitió ni se declara
verde. No se modificaron servicios activos.

## Observaciones y autorización por conexión — 16/11

La migración aditiva 3 normaliza el escenario en conexiones, corridas y
observaciones con clave conexión/colección/ID externo; conserva el archivo
anterior, fechas y preferencias. No modifica las migraciones aplicadas 1/2.
Las lecturas HTTP pasan a consultar observaciones unidas con concesiones,
dentro de una transacción de lectura. La consulta SQL Python confirmó que
la población son registros Identity: siete en Acme para seis personas, con
dos unknown para Eve. Se corrigió el agregado inicial de seis correos únicos
y la atribución de su equipo en cobertura; su observación Netskope se conserva
en la ficha. Se probaron también redondeos de mitad decimal como en Python.
El archivo SQLite configura foreign_keys,
busy_timeout y WAL en cada nueva conexión del driver, incluso si se reemplaza.

El laboratorio distingue principales por emisor/sujeto sintético y aplica
habilitación y lectura por conexión en cada petición. Búsquedas, fichas,
conexiones y capacidades omiten fuentes no autorizadas. Cobertura/denominadores
requieren ambas fuentes y la regla del par seleccionado; si no, devuelven 403
sin revelar existencia del correo. No hay cruces globales por email ni bypass
de la credencial inicial. Las preferencias quedan aisladas por principal.

issue-lab crea una credencial aleatoria en un archivo nuevo 0600, sin imprimirla;
la base guarda sólo su hash. La concesión y la auditoría son atómicas. Reemitir
sobre un sujeto existente falla, no amplía acceso. revoke-lab deshabilita al
principal con auditoría y se aplica en la siguiente petición. Son comandos
administrativos del laboratorio, sin endpoints de administración públicos.

verify:go pasó: vet, race y build. Pruebas de IDs repetidos entre conexiones
del mismo proveedor y entre colecciones, permiso insuficiente, aislamiento de
preferencias, fuentes incompletas/ausentes, duplicados ambiguos, pares sin regla,
revocación y rollback por fallo de auditoría. verify:go:web pasó con dos usuarios
sintéticos de alcances distintos: el de Identity navega sin pedir ni obtener
cobertura cruzada, el alcance sobrevive al reinicio y la revocación se verifica
con el servidor en ejecución. npm run verify pasó: documentos, 18 casos del
modelo y 475 unitarias web.

La suite amplia de 584 e2e no se repitió: esta entrega no cambia la UI y sus
ocho residuales anteriores siguen registrados en pendientes. No se acredita
identidad OIDC/Ozal, sincronización, conectores reales, jobs/exports, operación
empresarial ni distribución final. Sin commit, push, publicación o servicios
activos modificados.


## Personas en navegador contra Go y SQLite — 16/11

El servidor sirve opcionalmente el build real de web/ mediante --web-dir,
con fallback de navegación y errores de API conservados; sólo expone tipos
estáticos del build y rechaza archivos ocultos/base de datos. Personas deja de
cargar recursos PAM por compartir su runtime. El laboratorio anuncia lectura
de Identity y Netskope, sin permisos PAM ni mecanismos de identidad productivos.

Nueva migración aditiva: preferencias propias persistidas y auditadas en una
transacción. La lectura del cuerpo HTTP ocurre antes de tomar la conexión SQL;
una falla de auditoría revierte el cambio. migrate-lab actualiza bases anteriores
sin cambiar la fecha de captura; serve-lab rechaza esquemas incompatibles.

verify:go pasó con vet, race y build. verify:go:web pasó con frontend real en
build temporal, binario sin CGO y SQLite descartable: Alice/brecha, Bob/parcial,
Eve/por verificar, requests HTTP reales, denominadores Python, preferencias y
captura conservadas tras reiniciar, rechazo de headers de identidad falsos.
La credencial sintética se inyecta desde el harness; no se implementó login
interactivo ni autorización por conexión. No hay datos ni conectores reales.

verify:web pasó tipos, lint, estilos, 475 unitarias y build; en navegador
terminó con 576/584 correctas y ocho fallos: seis repeticiones por tema de una
expectativa de dos pestañas frente a las tres actuales, un calendario que espera
octubre frente al mes actual y la posición de un menú. La comparación en copia
temporal con el router original reprodujo las tres variantes de pestañas y el
calendario; el menú pasó en esa copia pero volvió a fallar en el árbol actual
por 2,51 px frente a la tolerancia de 2 px y queda sin resolver. No se modificaron esas pruebas ni las
pantallas afectadas. Los residuales quedan en el tracker y el gate completo
no se considera verde. No se declara paridad completa ni distribución empresarial.
Sin commit, push, publicación ni modificaciones de servicios activos.


## Laboratorio Go con SQLite — 16/11

Entrega parcial del backend, sin cerrar su pendiente: módulo Go con driver
SQLite fijado, init-lab/serve-lab, snapshot sintético persistido y cuatro lecturas
de Personas/cobertura. El núcleo traslada los casos de clasificación/frescura
Python. [Matriz y límites](PARITY.md) y [ejecución local](../server/README.md).

Verificación: npm run verify:go pasó (vet, tests con race y build). npm run
verify pasó: documentación, 18 tests del modelo y 475 unitarias web. La primera
corrida dentro del sandbox falló en la prueba que lanza el descubridor de tests;
la repetición fuera del sandbox pasó sin cambiar esa prueba. No se ejecutó
la suite e2e web ni el navegador contra Go. Sin conexión real, migración de
bases anteriores, cambios de servicios, commit, push o publicación.

La decisión SQLite actualiza arquitectura y pendientes: instalación de una
sola instancia sobre disco local. No se declaran permisos por conexión,
auditoría transaccional, worker, backup/restore o instalador empresarial como
implementados por este laboratorio.


## Correcciones de la revisión de merge — 22/10

| ID | Fecha UTC | Entrega | Evidencia y límite |
|---|---|---|---|
| `PROD-MERGE-001` | 2026-10-22 | Aislamiento inequívoco de claves de registros y líneas base; fechas según captura; búsqueda por columnas visibles y vínculos sin alterar atributos crudos; atribución de fuentes corregida; retiradas afirmaciones de código abierto del sitio y login mientras no se decida licencia. | Regresión de IDs opacos, revisión humana y retiro aislado; modelos del experimento (18) y web (6). `maintenance/v1` conserva `f6a7acc` en el checkout original; el producto se integra en `main` desde su worktree. No modifica servicios, credenciales, bases ni despliegue público. |

Verificación final: 11 e2e de web (43,9 s) y 16 del experimento (39,7 s),
todos correctos; typecheck/build de ambos paquetes y documentación correctos.
La primera corrida simultánea agotó 30 segundos en los casos largos de axe,
sin reportar violaciones; la corrida final fue en serie con un worker y
60 segundos por caso. Las opciones son de ejecución local, sin cambiar los
límites de la suite. Web usó Chromium 1248 mediante `CUSTODIA_CHROMIUM_PATH`,
con bibliotecas temporales mediante `LD_LIBRARY_PATH` y sin instalar paquetes.
El informe del workspace `reviews/custodia-merge-2026-10-22/report.md`
registra la integración local y sus referencias Git.
Los pendientes de Go, autenticación, distribución y paridad siguen abiertos.

## Aceptación de la base — 22/10

El dueño revisó Faro Digital y acepta avanzar desde esta base. Pide una
nueva vuelta al estilo y autoriza el commit local. El refinamiento queda en
PROD-STYLE-001; PROD-UI-001 continúa abierto por los recorridos restantes.
Esta aceptación no declara diseño final, paridad con Python ni distribución.

Antes del commit se repitieron `npm run verify`, `npm run verify:company`
(8 pruebas Chromium) y `npm run verify:demo` (16 del experimento anterior),
todos verdes. `npm run package:demo` verificó y reutilizó el paquete histórico
con su manifiesto; no empaqueta Faro Digital ni publica archivos. El chequeo
del diff preparado para commit pasó tras normalizar finales de archivo.

## Empresa ficticia sobre UI original — 22/10

[Faro Digital](COMPANY-DEMO.md) es el primer recorrido de PROD-UI-001,
que sigue abierto. `company-demo/` recupera el frontend de `f6a7acc` sin
su configuración de despliegue; añade datos nuevos compartidos por personas,
equipos, Safes y cuentas. Conserva shell, diseño, búsqueda, vista radial,
cadena PAM, inventarios y detalle. Contexto de empresa y enlaces a casos
visibles; modo de lectura, fuentes completa/parcial/vencida y sesión simulada.

Siete pruebas del escenario verifican coherencia, totales y referencias,
ambigüedad, cruces de vistas, filtros/paginación, observaciones anteriores a
la captura y rechazo explícito de escrituras/endpoints inexistentes.
El adaptador no tiene fallback al backend. La v1 no se modificó.

Typecheck y build verdes. Ocho pruebas Chromium contra el build real:
Persona → equipos, cobertura/brecha/ambigüedad, capturas parcial/vencida,
Safe con cuentas/clasificación deshabilitada/historial, cadena PAM, casos y
búsqueda, y axe A/AA de Personas. El recorrido de equipos se prueba con el
reloj del navegador en 2031 para comprobar que la fotografía fechada conserva
su interpretación. Inspección visual de la ficha de Ana y
[captura local](../presentation/company/person-ana.png). Axe manual de esa
vista con evidencia expandida: cero violaciones y cero comprobaciones
incompletas; no acredita todas las pantallas ni ambos temas.

Chromium se ejecutó con bibliotecas locales mediante `LD_LIBRARY_PATH` y
`CUSTODIA_CHROMIUM_PATH`, sin instalación de paquetes del sistema. Es una
particularidad del entorno de pruebas, no una dependencia de la demo servida.

Sin backend Go, base, autenticación real, ingesta o conector CrowdStrike.
Los módulos originales no incluidos todavía requieren adaptación o retiro;
no se declara paridad completa, limpieza terminada ni distribución aprobada.
El experimento reducido y sus paquetes/capturas quedan como material histórico.

## Corrección de dirección — 22/10

El dueño aclaró que prioriza la UI y experiencia originales y que cruces e
inferencias son centrales al inventario multi-herramienta. La demo reducida y el
material registrados abajo son exploratorios; sus pruebas no acreditan
paridad ni aprobación del producto. Ya no se exige aceptar esa demo para Go.

[Revisión dirigida del código Python/React](EXISTING-PRODUCT.md): registro de
herramientas, ingesta y clasificación de Safes, Personas, puentes Identity/PAM,
cobertura Identity/Netskope, cobertura de destinos y postura. Pasaron 24
pruebas Python seleccionadas y cuatro archivos de pruebas TS del frontend
original. Sin gate completo, consulta productiva ni cambios al checkout v1.
Pendientes dueños: PROD-UI-001, PROD-PARITY-001, PROD-CORR-001 y PROD-CS-001.

| ID | Fecha UTC | Entrega | Evidencia y límite |
|---|---|---|---|
| `PROD-PLAN-001` | 2026-10-22 | Base documental nueva: producto, arquitectura Go, conexiones, acceso, diseño y aceptación. | Revisión estática de la v1 `f6a7acc` y del contrato actual de visitante de Switchboard. Separadas decisiones del dueño de recomendaciones. No hay código Go, demo, integración OIDC ni SSO implementados. |
| `PROD-CLEAN-001` | 2026-10-22 | Retiro del árbol heredado y reemplazo por la base del producto en `product/go-reset`. | Worktree separado, sin configurar runtime. `npm run verify` verde: 12 documentos, 33 enlaces locales y 15 IDs únicos. Verificador sin dependencias; sólo documentación. No se cambian servicios/bases de v1, ni se sanea historia Git, ni se publica. |
| `PROD-FIX-001` | 2026-10-22 | Guion del laboratorio como contrato: [fixture](../fixtures/lab/scenario.json) con dos conexiones, cinco corridas y lo esperado en cada paso; modelo puro en TypeScript (`demo/src/model/`) con observaciones, cambios y revisión en capas separadas. | `npm run verify`: los siete pasos contra lo esperado escrito a mano, más invariantes (Sur intacto, decisión conservada, reset exacto, sin mutación, alcance nuevo sin bajas, reaparición, parcial sin línea base, entradas rechazadas). Sin dependencias ni red. El backend Go deberá pasar el mismo fixture; no demuestra UI ni persistencia. |

`PROD-PLAN-001` y `PROD-CLEAN-001` quedaron en el commit `5dec025`. Esta tabla describe
la capacidad del árbol local; no demuestra una instalación pública ni un
producto ejecutable. Los siguientes lotes están en [pendientes](TO_IMPLEMENT.md).

## Revisión técnica y material local — 22/10

Sobre `94b976f`, sin nuevos commits: corregidos aviso parcial obsoleto tras
completa, detalle de otra conexión al filtrar, foco del guion tras apertura
manual y exceso de controles en el primer viewport móvil. El modelo de
inventario y su fixture no cambian.

Verificados los 17 casos del modelo, build y 16 pruebas de navegador
(corrida final completa, incluyendo el aviso de terceros).
Axe recorre los seis estados en ES y EN. Inspección visual con capturas a
320, 375, 768, 1024 y 1440 px; sin overflow a nivel página en las mediciones.
Esto no certifica toda WCAG ni sustituye aceptación del dueño.

Disponibles [textos y capturas](PRESENTATION.md), avisos de dependencias en el
build y `npm run package:demo`: paquete sólo de archivos públicos, con
manifiesto SHA-256 y verificación al reutilizarlo. Probado desde una subruta
real en un servidor estático local. Contenido ES/EN y captura integrados en
el checkout local de Portfolio; sin enlace de demo pública ni publicación.

`PROD-DEMO-001`, `PROD-DIST-001` y `PROD-SHOW-001` siguen en el tracker con
sus aceptaciones pendientes. No hay backend Go ni autenticación nuevos.
