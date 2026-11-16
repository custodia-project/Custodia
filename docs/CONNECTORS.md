---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Conectores: una experiencia común, contratos específicos

**Estado:** contrato de producto. Dos adaptadores implementados y probados
contra servidores HTTP locales sintéticos: el contrato normalizado
`http-inventory-v1` y el contrato CyberArk PAM `cyberark-pam-safes-v1`
(colecciones `safes`, `safe_members`, `accounts`, `platforms`, `psm_servers`,
`psm_connectors` y `platform_psm_links`). El alta de conexiones y el probe por
API ya existen para un administrador; las herramientas planificadas no tienen
contrato y no se pueden conectar. Ningún adaptador acredita un tenant real.
Objetivo: agregar una herramienta sin modificar el núcleo ni obligar al
operador a conocer sus endpoints. No es posible generalizar los significados
de todas las APIs con un formulario URL + token.

## Tres identidades distintas

**Proveedor** identifica quién ofrece una herramienta. **Tipo de conector**
identifica el contrato que sabe leerla. **Conexión** identifica la instancia
configurada. Dos tenants de una misma herramienta son dos conexiones, con
secretos, checkpoints, permisos y corridas independientes.

Un registro estático de tipos en el binario declara las capacidades
disponibles. Sin carga de código de terceros, plugins dinámicos ni scripts
arbitrarios en la primera versión. Un tipo nuevo requiere contrato, fixtures
y pruebas; que aparezca en un catálogo no significa que esté soportado.

## Contrato de un adaptador

| Operación | Resultado y límite |
|---|---|
| Describir | ID/versionado, configuración, referencias requeridas y colecciones posibles. |
| Validar configuración | Errores locales sin red y sin revelar secretos. |
| Comprobar acceso | Lectura mínima documentada; separa conectividad, autenticación y permisos de cada capacidad. Un 200 no acredita todo el conector. |
| Extraer colección | Emite observaciones validadas con paginación y buffers acotados; responde a cancelación. |
| Finalizar extracción | Recibo de alcance, versión de esquema, estado, cantidades y evidencia de completitud. El adaptador no decide bajas ni modifica revisión humana. |

El núcleo controla jobs, persistencia, autorización, publicación, comparación,
reconciliación y auditoría. El adaptador controla autenticación externa,
endpoints, paginación, selección de campos y traducción semántica. Definir
las interfaces Go exactas junto con su primer consumidor y prueba.

## Experiencia del operador

1. Elegir tipo soportado y nombre local de conexión.
2. Indicar URL base y una referencia de credencial; mostrar el alcance de
   lectura solicitado, no campos técnicos por defecto sin explicación.
3. Comprobar acceso y revisar capacidades disponibles, denegadas o desconocidas.
4. Seleccionar colecciones y alcance. Ver la retención y coste de consulta.
5. Ejecutar primera sincronización; ver filas observadas, descartes y errores.
6. Consultar última ejecución completa y cambios; reintentar sin duplicar.

Custodia se instala vacía: no hay fuentes ni credenciales por defecto. El
operador elige la herramienta, pega la URL del tenant y una referencia de
credencial, prueba el acceso y recién después recolecta. Un tenant pertenece a
la instalación del operador; Custodia no es un servicio multiinquilino central.

Capacidades de inventario, métricas agregadas, detalle sensible y catálogo
en vivo son distintas. Una conexión puede servir sólo algunas. Una vista
sólo se ofrece si su capacidad y permiso están disponibles. Un catálogo
consultado en vivo no se presenta como snapshot histórico.

## Alta de conexiones y catálogo

`GET /api/ingestion/connectors` publica el registro estático. Cada descriptor
declara estado, modos de autenticación, colecciones y endpoints de probe. Un
conector `planned` (Netskope, QRadar, Palo Alto, FortiGate, CrowdStrike,
Tenable) no declara endpoints, colecciones ni credenciales: es una opción de
hoja de ruta, no un contrato, y el alta lo rechaza. Esto evita inventar API o
pedir permisos por una herramienta que todavía no se implementó.

Un administrador conecta un tenant sin tocar el host:

```sh
POST /api/ingestion/connections
{
  "connector": "cyberark-pam-safes-v1",
  "base_url": "https://pvwa.empresa.example/PasswordVault",
  "collection": "safes",
  "scope": "pam-safes-prod",
  "auth_mode": "oauth-client-credentials",
  "token_url": "https://pvwa.empresa.example/oauth2/platformtoken",
  "client_id_ref": "env:CUSTODIA_PAM_CLIENT_ID",
  "client_secret_ref": "secret:pam-client-secret",
  "scopes": ["pam.read"],
  "ca_file": "/etc/custodia/empresa-ca.pem"
}
```

Reglas del alta:

- Sólo un administrador puede crear o probar una conexión.
- La URL de un conector implementado debe ser HTTPS, sin usuario, query ni
  fragmento; se admite un prefijo de path (por ejemplo `/PasswordVault`).
- La credencial es siempre una referencia `env:`, `file:` o `secret:`; el alta
  rechaza valores literales y nunca contacta el tenant.
- El par conector/colección y el modo de auth deben existir en el descriptor.
- El creador recibe lectura y `can_run` para esa conexión, y el alta queda en
  auditoría (`connection_register`).
- La respuesta no repite la referencia de credencial: sólo indica si está
  configurada.

`POST /api/ingestion/connections/{id}/probe` ejecuta el mismo plan validado del
probe sobre los endpoints que esa colección necesita, devuelve un reporte
saneado y audita `connection_probe`. Un probe con fallos se informa endpoint
por endpoint; no publica observaciones ni encola una corrida.


## Estados y reconciliación

Estados por colección: sin configurar, pendiente, ejecutando, completa,
parcial, fallida o cancelada. Credencial inválida y permiso insuficiente
tienen diagnósticos separados. Mostrar cuándo se verificó cada condición.

Para retirar un registro ausente se exige una extracción completa de la
misma conexión, colección, alcance y generación contractual. Páginas faltantes,
cursor repetido, IDs contradictorios, total inconsistente, timeout o 403
impiden afirmar completitud. Un resultado vacío sólo es cero si el contrato
lo acredita; no se infiere de un error.

Una interrupción puede dejar staging y un cursor recuperable, nunca un
snapshot publicado como completo. Reintento/checkpoint respeta vencimiento
del cursor y el contrato de cada proveedor. Cambiar un filtro inicia nueva
línea base; no inventa bajas históricas.

## Transporte y protección de datos

HTTPS verificado; CA adicional sólo por configuración explícita. La URL base
y los destinos admitidos pertenecen a la configuración administrativa. No
seguir URLs de paginación ni redirects a otro origen enviando credenciales.
Acceso a redes privadas, si el despliegue lo requiere, se declara por
conexión; una solicitud de un lector no puede elegir un destino nuevo.

Timeout total, tamaño de respuesta, cantidad de páginas/filas y concurrencia
acotados; backoff y presupuesto para retry. Respetar límites y Retry-After;
autenticación fallida no dispara un bucle ilimitado. POST puede representar
una consulta read-only si el contrato del proveedor lo especifica: no
confundir método HTTP con autorización para mutar el sistema externo.

Cursores e índices de exportación pertenecen a esta conexión/tarea, nunca
se comparten con otra integración externa. Recolección mínima por allowlist;
excluir credenciales de cuentas, contenido DLP, texto libre y payloads
completos salvo contrato explícito posterior. La evidencia reproducible es
una proyección saneada con versión y procedencia, no una copia ilimitada.

## Contrato de aceptación reutilizable

Todo adaptador ejecuta los mismos escenarios: extracción completa, cero
confirmado, segunda página fallida, duplicado idéntico/contradictorio,
403, 429, cancelación, cursor repetido, cambio de alcance y retry idempotente.
Comprobar que no filtra secretos y que una corrida parcial conserva recursos
anteriores. Un proveedor real necesita además fixtures propios y aceptación
de lectura en un entorno autorizado. El conector de laboratorio se rotula
como simulado y no acredita soporte de un fabricante.

## Primer adaptador de laboratorio

`http-inventory-v1` es un contrato interno y sintético, no una emulación de
Netskope, CyberArk ni otro fabricante. Sólo acepta una URL `http://` con IP
numérica de loopback, sin usuario, query ni fragmento, y no envía
credenciales, cookies ni sigue redirects. La conexión declara una colección
(`users` o `devices`), alcance y generación; la clave persistida es conexión +
colección + ID externo.

El servidor local responde a
`GET {base}/v1/collections/{collection}?limit=N&cursor=OPACO` con
`{"objects":[...],"next_cursor":""}`. Ambos campos son obligatorios:
`objects` nulo/ausente y `next_cursor` nulo/ausente se rechazan; sólo
`objects:[]` con `next_cursor:""` explícitos acreditan una colección vacía.
`next_cursor` vacío acredita el fin de la colección. Los atributos están
sujetos a allowlist: `users` exige `email` y admite `name`; `devices` exige
`user_id` y admite `last_signal_at` RFC3339. Se rechazan atributos extra,
IDs vacíos o fuera de límite, JSON inválido, cursor repetido, página mayor
que el límite pedido, cuerpo mayor a 1 MiB, content type distinto y códigos
HTTP distintos de 200. Los errores guardan sólo un código estable y un
detalle saneado, nunca el cuerpo del proveedor.

Límites por conexión: página 1–500, hasta 1.000 páginas y 100.000 registros.
Un fallo de transporte o HTTP después de una página produce `partial` con lo
recibido; un fallo de integridad o un fallo en la primera página no publica
observaciones. Sólo una corrida `completed` del mismo alcance y generación
que la línea base puede retirar ausentes. La configuración se realiza en el
host con `add-http-lab`; el intervalo `0` deshabilita la agenda y un valor de
5–525600 minutos la habilita. `serve`/`serve-lab` inician un worker que ejecuta
la cola con heartbeat/lease; la API sólo encola y la UI consulta. Las cuentas
y secretos reales, la configuración por API y el probe de proveedor quedan
fuera de este tramo.

## Segundo adaptador: CyberArk PAM Safes (fixture)

`cyberark-pam-safes-v1` implementa la forma documentada de
`GET /API/Safes?limit=N`: `{"value":[...],"count":N,"nextLink":...}`. El
`nextLink` debe pertenecer al mismo origen y a `/API/Safes`; no se siguen
redirecciones, URLs externas, fragmentos ni credenciales embebidas. `count` es
el total esperado y el runner rechaza una colección incompleta o un total que
cambie entre páginas. La allowlist de metadatos es:

```text
safeNumber (ID externo), safeName, safeUrlId, location, creatorName,
olacEnabled, numberOfVersionsRetention, numberOfDaysRetention,
autoPurgeEnabled, creationTime, lastModificationTime, description,
managingCPM, isExpiredMember
```

Los epochs se normalizan a RFC3339; los campos fuera de la allowlist se
descartan. Se excluyen cuentas, secretos, tickets, sesiones y el payload
completo. La primera página 401/403 falla sin publicar; un error de una página
posterior deja `partial` con lo leído y nunca retira Safes.

El mismo conector puede configurarse como una segunda conexión con la
colección `safe_members`. El adaptador enumera primero `/API/Safes` completo y
luego pagina `GET /API/Safes/{safeName}/Members?limit=N` para cada Safe. El cursor de la
colección es opaco y combina el índice de Safe con el `nextLink` del proveedor;
un cursor que cambie de Safe u origen se rechaza. La allowlist de miembro es
`safeNumber`, `safeName` (padre), `memberId`, `memberName`, `memberType`,
`membershipExpirationDate`, `isExpiredMembershipEnable`, `isPredefinedUser`,
`isReadOnly` y `permissions`. El ID externo es `safeNumber:memberId`; los
permisos se guardan como objeto JSON acotado. La enumeración de Safes está
acotada a 100 páginas y 10.000 Safes; se rechazan duplicados. Se valida el
total de miembros por Safe y el total agregado: una lista corta o un total
cambiante falla sin publicar. Un Safe sin miembros sólo acredita cero con
`value:[]` y `count:0` explícitos.

La colección `accounts` lee `/API/Accounts?limit=N` y resuelve el Safe padre
por nombre contra la misma enumeración acotada. La allowlist es `id` (o
`accountId`/`account_id`), `name`/`accountName`/`userName` como nombre de
cuenta, `userName`, `address`, `safeName`, `platformId`, `accountType`,
`category` y el subconjunto `secretManagement` con
`automaticManagementEnabled`, `status` normalizado (`success`, `failure`,
`in_process`) y `lastModifiedTime` convertido a RFC3339. El ID externo es
`safeNumber:accountId`. Se excluyen contraseñas, `platformAccountProperties`,
`manualManagementReason`, `lastModifiedBy`, `secretType` y el payload. Una
cuenta sin Safe visible o sin ID se omite y la corrida termina `partial` con
aviso `skipped_accounts`: publica lo leído pero no retira cuentas ni reemplaza
la línea base, igual que la supresión de reconciliación de DS Hub.

La colección `platforms` lee `/API/Platforms`, que el contrato verificado
devuelve `{Platforms:[...], Total:N}` en una sola respuesta. El adaptador
valida `Total` contra la lista y luego sirve páginas locales acotadas con un
cursor propio; no inventa parámetros de paginación del proveedor. La allowlist
es `general.id` (ID externo), `general.name` (o el ID como fallback),
`general.active`, `general.platformBaseID`, `general.platformType`,
`general.systemType` y `sessionManagement.PSMServerID`. Se excluyen
`properties`, políticas, reglas de contraseña y el payload. El detalle
`/API/Platforms/{id}` y `Details.AllowedSafes` quedan fuera de este tramo.
`psm_server_id` es una referencia textual; el cruce explicable se publica en
la colección `platform_psm_links`, no se infiere al leer.

Las colecciones `psm_servers` y `psm_connectors` implementan los catálogos
verificados `GET /API/PSM/Servers?limit=1000` y
`GET /API/PSM/Connectors?limit=1000`, con respuestas
`{PSMServers:[...], Total:N}` y `{PSMConnectors:[...], Total:N}`. El adaptador
valida `Total` y sirve páginas locales acotadas. La allowlist de servidor es
`ID` (ID externo), `Name` (fallback al ID) y `Address`; la de conector es
`ID` (ID externo) y `DisplayName` (fallback al ID). Se excluyen propiedades,
tipos técnicos no allowlisted, capacidad, salud y el payload.

`platform_psm_links` cruza en una corrida la referencia
`sessionManagement.PSMServerID` de cada plataforma contra `psm_servers`:

- `exact`: la referencia coincide literalmente con un ID del catálogo.
- `casefold`: hay un único ID distinto que coincide ignorando mayúsculas.
- `ambiguous`: hay dos o más IDs distintos que sólo difieren en mayúsculas; se
  publica la fila con `resolution=ambiguous` y sin objetivo elegido.
- `missing`: no hay candidato; se publica la referencia cruda con
  `resolution=missing`.

El ID externo es `platformID:referencePSMID`; la allowlist es `platform_id`,
`platform_name`, `reference_psm_id`, `psm_server_id`, `psm_server_name` y
`resolution`. Una fila `ambiguous` no es una arista: deja evidencia de por qué
no se eligió un PSM. Una `missing` conserva el ID crudo y no inventa un
servidor del catálogo. Sólo una corrida completa retira vínculos cuyas
referencias desaparecieron; parcial y fallida no retiran. El cruce no usa
nombres de plataforma, prefijos ni similitud.

Los miembros, las cuentas, las plataformas y los catálogos PSM son
colecciones separadas, no relaciones inventadas entre sí, con roles o con
PSM. La cadena `users → roles → safes → platforms → psm` completa, el detalle
de plataforma y el probe/TLS siguen en tramos posteriores.

Configuración de un fixture local:

```sh
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-safes \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection safes --scope synthetic-safes-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-members \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection safe_members --scope synthetic-members-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-accounts \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection accounts --scope synthetic-accounts-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-platforms \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection platforms --scope synthetic-platforms-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-psm-servers \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection psm_servers --scope synthetic-psm-servers-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-psm-connectors \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection psm_connectors --scope synthetic-psm-connectors-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection pam-psm-links \
  --connector cyberark-pam-safes-v1 --url http://127.0.0.1:18080 \
  --collection platform_psm_links --scope synthetic-psm-links-v1 \
  --generation cyberark-pam-safes-v1 --page-size 2 --grant synthetic-lab
```

Este tramo no usa TLS ni credenciales de CyberArk y no debe apuntarse a un
tenant real. El probe TLS de lectura está probado contra fixtures locales y
cableado al comando `probe-pam` con bearer, OAuth client-credentials, mTLS y
referencias `env:`/`file:`/`secret:`. La validación del contrato real,
autorización de tenant y aceptación en un entorno autorizado siguen pendientes
de `PROD-REAL-001`. El contrato está en [PROBE.md](PROBE.md).
