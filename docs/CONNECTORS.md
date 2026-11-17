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
contrato y no se pueden conectar. El inventario calcula, al leer sobre datos
publicados, las métricas que cada conector declara en el binario. Ningún
adaptador acredita un tenant real. Objetivo: agregar una herramienta sin
modificar el núcleo ni obligar al operador a conocer sus endpoints. No es
posible generalizar los significados de todas las APIs con un formulario
URL + token.

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
  "collections": ["safes", "safe_members", "accounts"],
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
  rechaza valores literales y nunca contacta el tenant. `file:` y `secret:` se
  resuelven sólo dentro de `CUSTODIA_PROBE_SECRET_DIR`, nunca contra una ruta
  arbitraria del host.
- Cada colección elegida debe existir en el descriptor; el alta deduplica y
  crea una fila de fuente por colección bajo un mismo `group_id`.
- Todas las colecciones comparten URL, credencial, CA y auth; el operador
  conecta el tenant **una vez**.
- El creador recibe lectura y `can_run` para esa conexión, y el alta queda en
  auditoría (`connection_register`).
- La respuesta no repite la referencia de credencial: sólo indica si está
  configurada.

### Secretos de conexión y gestión

El alta acepta un secreto escrito en la consola además de las referencias
`env:`, `file:` y `secret:`. El valor se cifra antes de tocar SQLite con
XChaCha20-Poly1305; la clave maestra de 32 bytes vive fuera de la base, en
`master.key` (0600) dentro del directorio de datos o en la ruta que indique el
instalador. El AAD liga la versión del formato, el `group_id`, el campo y la
versión del secreto; un secreto movido a otra conexión o reemplazado por una
versión anterior no descifra. La API, la auditoría y los logs
nunca devuelven el valor: sólo `secret_configured` y la versión.

- `POST /api/ingestion/connections` acepta `secret` (write-only) para
  `bearer`; si se usa, la fuente guarda la referencia interna
  `vault:<group_id>:auth` y el alta rechaza un valor literal en
  `credential_ref`. Las referencias externas siguen funcionando. El cliente
  nunca elige una referencia `vault:`: el alta rechaza cualquiera (en
  `credential_ref`, `client_id_ref` o `client_secret_ref`), la edición sólo
  admite la de la misma conexión y el resolver del probe y del worker se
  niega a descifrar el secreto de otra conexión aunque la referencia llegue
  a la base. Un secreto sólo lo envía su propia conexión.
- `PUT /api/ingestion/connections/{group_id}` edita URL, auth, CA, límites y
  agenda, y acepta `secret` para rotarlo en la misma transacción: si la
  edición se rechaza, el secreto no cambia. Cambiar URL o autenticación
  invalida el probe anterior y apaga la agenda. Cambiar el origen (esquema,
  host o puerto) de una conexión con secreto cifrado exige enviar el secreto
  otra vez (409); un cambio de path en el mismo origen lo conserva.
- `POST /api/ingestion/connections/{group_id}/secret` rota el secreto, sube la
  versión e invalida probe y agenda.
- `POST /api/ingestion/connections/{group_id}/pause|resume|retire` pausa,
  reanuda o retira. Retirar conserva historial y auditoría, apaga la agenda y
  oculta la conexión de las vistas y del worker; no borra observaciones.
- `GET /api/ingestion/connections` lista los grupos visibles para el
  administrador con `secret_configured`, estado, última prueba y agenda. Nunca
  incluye el secreto ni la referencia interna.
- La agenda sólo se activa después de un probe correcto de la configuración
  vigente. Cualquier cambio de URL, auth, CA o secreto la apaga y exige un
  probe nuevo.
- `init` genera `master.key`; `serve --master-key RUTA` la carga (por defecto
  `<dir>/master.key`). El backup de SQLite no incluye la clave: restaurar en
  otro host exige conservarla por separado. Sin clave, las conexiones con
  secreto cifrado no pueden probarse ni sincronizarse; el resto del producto
  sigue operativo.

`POST /api/ingestion/connections/{id}/probe` acepta el `group_id` o una fila
de colección. Con el grupo ejecuta el mismo plan validado sobre la unión de
endpoints de todas sus colecciones, exige que compartan URL/auth/CA y devuelve
un reporte saneado; audita `connection_probe`. Un probe con fallos se informa
endpoint por endpoint; no publica observaciones ni encola una corrida.

Cada colección conserva su propia corrida, línea base, agenda y permisos. El
grupo es la unidad que ve el operador; la separación interna evita mezclar
retiros de una colección con los de otra.


## Catálogo y prioridad (decisión del dueño, 16/11)

El catálogo lista todas las herramientas pedidas, pero el trabajo se
concentra en las **de código abierto con API documentada**: se pueden
instalar en un laboratorio propio y aceptar contra una instancia real, sin
esperar a que una empresa abra su tenant. Las comerciales quedan listadas
como `planned` hasta que un cliente dé acceso autorizado a su API.

Cada descriptor lleva `distribution` (`open-source` o `commercial`). Es una
etiqueta de catálogo; no acredita soporte ni contrato.

| Herramienta | Categoría | Distribución | Estado | Cómo se valida |
|---|---|---|---|---|
| CyberArk PAM | PAM | Comercial | Implementado contra fixture | Tenant autorizado de un cliente. |
| Netskope | SSE | Comercial | Planificado | Tenant de un cliente. |
| IBM QRadar | SIEM | Comercial | Planificado | Instancia de un cliente. |
| CrowdStrike Falcon | EDR | Comercial | Planificado | Tenant de un cliente. |
| FortiMail | Email security | Comercial | Planificado | Appliance o tenant de un cliente. |
| FortiGate | Network security | Comercial | Planificado | Appliance de un cliente. |
| Palo Alto Networks | Network security | Comercial | Planificado | Firewall/Panorama de un cliente. |
| Tenable | Vulnerability management | Comercial | Planificado | Tenant de un cliente. |
| Wazuh | SIEM/XDR | Código abierto | Implementado y aceptado contra 4.14.10 | Instancia propia con agente real. |
| Fleet (osquery) | Endpoint inventory | Código abierto | Candidato prioritario | Instancia propia. |
| DefectDojo | Vulnerability management | Código abierto | Candidato prioritario | Instancia propia. |
| CrowdSec | Intrusion prevention | Código abierto | Candidato | API local de una instancia propia. |
| OPNsense | Network security | Código abierto | Candidato | Instancia propia. |
| Keycloak | IAM | Código abierto | Candidato | Instancia propia. |
| MISP | Threat intelligence | Código abierto | Candidato | Instancia propia. |
| OpenBao | Secrets management | Código abierto | Candidato | Instancia propia; sólo metadatos, nunca valores. |

Las licencias, endpoints y modos de autenticación de los candidatos se
verifican contra la documentación oficial y una instancia propia antes de
aprobar su contrato; esta tabla no los fija. Llamar a la API de una
herramienta no incorpora su código: no copiar código ni fixtures de sus
repositorios sin revisar la licencia. La instancia de laboratorio se usa con
datos sintéticos.

Pasos para pasar un candidato a implementado: contrato (endpoints,
allowlist, paginación, autenticación de lectura), fixture sintético, adaptador
con el contrato de aceptación común, métricas declaradas y aceptación contra
la instancia propia. Un conector por vez; no abrir varios a medias.

## Métricas por conector (decisión del dueño, 16/11)

Cada conector declara sus métricas en el binario, junto a sus colecciones.
El administrador no las define en el MVP; las métricas personalizadas
quedan para después.

Una métrica declara: ID versionado, nombre y explicación legibles,
colección de origen, cálculo (conteo, conteo con filtro sobre un campo de la
allowlist, proporción con numerador y denominador, antigüedad respecto de un
umbral fijo) y unidad. Cambiar el cálculo crea una versión nueva; no se
reinterpreta un valor histórico. El binario trae el catálogo declarado en
`server/internal/metrics`; la validación de pruebas exige que la colección
pertenezca al descriptor del conector y que cada campo usado exista en la
allowlist normalizada de `server/internal/inventory`. Una métrica no puede
leer el payload crudo del proveedor ni un campo fuera de esa allowlist.

Cálculos disponibles:

| Cálculo | Semántica |
|---|---|
| `count` | Registros activos de la colección publicada. |
| `count_where` | Registros activos cuyo campo allowlisted es igual a uno de los valores declarados. Un registro sin el campo no cuenta. |
| `ratio` | Porcentaje del numerador (`where`) sobre el denominador (`denominator_where`, o todos los registros). Se redondea a un decimal. |
| `age_over` | Registros cuyo campo RFC3339 es anterior al umbral fijo de días, medido desde la fecha de publicación. Los registros sin fecha usable se informan como omitidos. |
| `count_without` | Registros de la colección de origen que ningún registro de una colección hermana de la misma conexión referencia por el campo declarado. |

Reglas de cálculo y presentación:

- Se calcula al leer sobre la última publicación de la colección en esa
  conexión; no hay tablas de métricas ni valores materializados. La
  publicación es la última corrida `completed` o `partial`, y sólo se leen
  las observaciones activas de su mismo alcance y generación: un cambio de
  alcance no mezcla la línea base anterior.
- Cada valor muestra la fecha de los datos (la publicación más antigua entre
  sus fuentes) y el estado de la corrida de cada fuente.
- Sin colección configurada o sin corrida que haya publicado datos, la métrica
  es «no disponible», nunca cero. Una corrida parcial conserva el valor y lo
  marca como parcial; una proporción o un `count_without` cuyo denominador o
  referencia son parciales no se calculan. Una corrida fallida no reemplaza
  la última publicación.
- Cero sólo es un valor cuando una publicación confirma que no hay registros
  que cumplan el cálculo.
- Cada valor permite abrir exactamente los registros que lo componen, con los
  mismos permisos de lectura de la conexión. Un usuario sin lectura no ve la
  conexión; una colección no legible se informa igual que una no configurada,
  sin revelar su existencia. Los umbrales son fijos en la versión de la
  métrica y se muestran en su explicación.
- El inventario por herramienta muestra conexión, colecciones, última
  corrida, completitud y sus métricas. No hay métricas combinadas entre
  herramientas en el MVP.

### Métricas declaradas de CyberArk PAM

`cyberark-pam-safes-v1` declara estas métricas sobre sus colecciones ya
implementadas. Los nombres de campo son los atributos normalizados publicados,
no los nombres JSON del proveedor.

| ID | Métrica | Colección | Cálculo | Unidad |
|---|---|---|---|---|
| `cyberark-pam.safes.total.v1` | Safes totales | `safes` | `count` | Safes |
| `cyberark-pam.safes.without_members.v1` | Safes sin miembros | `safes` ↔ `safe_members` | `count_without` de `safes` cuyo número no aparece como `safe_number` de ningún miembro | Safes |
| `cyberark-pam.accounts.total.v1` | Cuentas totales | `accounts` | `count` | cuentas |
| `cyberark-pam.accounts.auto_management_disabled.v1` | Cuentas con gestión automática deshabilitada | `accounts` | `count_where automatic_management_enabled=false` | cuentas |
| `cyberark-pam.accounts.auto_management_disabled_ratio.v1` | Proporción de cuentas sin gestión automática | `accounts` | `ratio` de `false` sobre las cuentas que informan `true` o `false` | porcentaje |
| `cyberark-pam.accounts.last_change_failed.v1` | Cuentas con último cambio fallido | `accounts` | `count_where management_status=failure` | cuentas |
| `cyberark-pam.platforms.inactive.v1` | Plataformas inactivas | `platforms` | `count_where active=false` | plataformas |
| `cyberark-pam.platforms.psm_unresolved.v1` | Plataformas con PSM sin resolver | `platform_psm_links` | `count_where resolution∈{missing,ambiguous}` | plataformas |

La última métrica usa el cruce ya publicado `platform_psm_links`; no infiere
la resolución al leer ni cuenta una plataforma sin referencia PSM. «Safes sin
miembros» exige las dos colecciones en la misma conexión y no se calcula si
`safe_members` es parcial. La proporción sólo usa el subconjunto que informa
la bandera; por eso su denominador puede ser menor que «Cuentas totales».

### API de inventario

Las dos rutas son de lectura, se sirven desde la misma sesión autenticada y
filtran por las concesiones de lectura antes de calcular:

- `GET /api/inventory/tools` devuelve las conexiones visibles con su
  conector, nombre, vendedor, categoría, `completeness`
  (`complete`, `partial`, `incomplete`, `unpublished`), última corrida,
  colecciones (estado de publicación, cantidad de registros y fechas) y
  métricas. Cada métrica incluye `status` (`available`, `partial`,
  `unavailable`), `value` (`null` cuando no hay valor), `partial`,
  `numerator`/`denominator` para proporciones, `reason` estable
  (`collection_unavailable`, `no_publication`, `partial_denominator`,
  `partial_reference`, `empty_denominator`), `data_as_of` y `sources` con la
  corrida de cada colección.
- `GET /api/inventory/metrics/{metric_id}/records?connection={id}&limit=N`
  devuelve la métrica recalculada y los registros que la componen. `connection`
  es el identificador de la herramienta (el grupo de colecciones que muestra
  el inventario), no la fila interna de una colección. `total` es la cantidad
  completa contada y `records` se corta en `limit` (1–1000; por defecto 200)
  con `truncated=true`. Una conexión no visible y una inexistente responden
  igual (404), para no revelar que existe.

Las rutas no aceptan escrituras (405) y no agregan tablas: el cálculo usa las
observaciones y corridas ya publicadas. El detalle nunca devuelve secretos,
payloads ni campos fuera de la allowlist.


## Tercer adaptador: Wazuh (agentes y grupos)

Contrato verificado contra la documentación oficial y el OpenAPI de Wazuh
4.x: [API](https://documentation.wazuh.com/current/user-manual/api/getting-started.html),
[referencia](https://documentation.wazuh.com/current/user-manual/api/reference.html),
[release 4.14.10](https://documentation.wazuh.com/current/release-notes/release-4-14-10.html)
y [spec.yaml](https://github.com/wazuh/wazuh/blob/master/api/api/spec/spec.yaml).
La instancia de aceptación es un `wazuh/wazuh-manager:4.14.10` propio con
`wazuh/wazuh-agent:4.14.10`; no es un tenant de cliente.

- Base HTTPS del manager (`:55000`), CA privada admitida por archivo.
- Login `POST /security/user/authenticate` con Basic auth; devuelve un JWT
  de 900 segundos. Custodia lo obtiene al preparar el probe o la corrida y lo
  mantiene sólo en memoria. No hay refresh ni token persistido: una corrida
  que exceda la vigencia falla cerrada y se reintenta.
- RBAC mínimo verificado: acciones `agent:read`, `group:read` y
  `manager:read`. La aceptación usó un usuario con una política propia de
  sólo lectura; `POST /agents` responde 403.
- Colecciones: `GET /agents` y `GET /groups`, con `limit`/`offset`/`sort`,
  `total_affected_items` y páginas de hasta 500. `GET /manager/info` sólo se
  usa en el probe.
- Allowlist de `agents`: id externo, nombre, `status` (`active`, `pending`,
  `never_connected`, `disconnected`), IP, IP de registro, SO (nombre,
  plataforma, versión, arquitectura), versión del agente, grupos (lista
  unida por coma), cantidad de grupos, último keepalive, alta, manager y
  nodo. Nada de configuración, claves ni payloads.
- Allowlist de `groups`: nombre y cantidad de agentes.
- **Las alertas no están en esta API**: viven en el indexer. Este adaptador no
  lee el indexer ni inventa métricas de detecciones.

Métricas declaradas: agentes totales, activos, pendientes, desconectados,
nunca conectados, sin contacto hace más de 7 días (`age_over` sobre
`last_keep_alive`), grupos totales y grupos sin agentes (`agent_count = 0`).
Un agente sin keepalive no cuenta como antiguo.

Aceptación ejecutada el 16/11 contra la instancia propia: 4 agentes (1 activo,
1 desconectado, 2 nunca conectados) y 3 grupos (2 vacíos); probe con 0
endpoints fallidos; publicaciones completas de 4 y 3 registros; métricas
4/1/0/1/2/0/3/2. El recorrido de alta, secreto cifrado, CA, probe, agenda e
inventario se repitió desde la consola.

Límites: un solo manager (los nodos de cluster quedan para después), sin
alertas/indexer, sin escrituras ni acciones sobre agentes, y el JWT se renueva
por corrida. Un manager real con otra versión mayor requiere revisar el
contrato antes de declararlo disponible.

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
