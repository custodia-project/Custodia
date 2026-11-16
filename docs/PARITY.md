---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Recorridos Go: Personas/cobertura y sincronización

Referencia: servicio Python person_client_coverage y sus tests en f6a7acc;
contratos HTTP tipados en web/src/cross/lib/chains-api.generated.ts. Nunca
se usan inventarios privados. El código Go está en server/ y es un laboratorio
sintético explícito, no la aceptación de un backend empresarial.

| Recorrido | Ruta GET bajo /api | Implementación y límite |
|---|---|---|
| Buscar persona | /chains/person/search?q= | Filtra nombre/correo del escenario Acme. Sólo muestra fuentes del laboratorio. Consulta únicamente observaciones de las conexiones autorizadas. |
| Ficha | /chains/person?email= | Identity y Netskope, vínculos únicos o ambiguos y dispositivos recientes/no recientes. PAM/SIA y los catálogos auxiliares quedan pendientes. |
| Resumen de cobertura | /chains/person/coverage | Estados y porcentajes con denominadores Python; fuentes completas vigentes, umbrales 24 h y 7 días. |
| Evidencia individual | /chains/person/coverage/detail?email= | Campos del contrato, explicación, fuentes y not_applicable fuera de población con evidencia vigente; unknown con evidencia vencida. |
| Acceso de laboratorio | /auth/status | Credencial local de principal sintético; capacidades derivadas de concesiones por conexión. No demuestra identidad empresarial. |

## Reglas trasladadas

Clasificación pura: enlace único de Identity/Netskope, dispositivos ambiguos,
covered, partial, gap y unknown. Captura ausente, parcial, fallida, futura o de
más de 24 h invalida la evaluación. El límite exacto de 24 h sigue vigente.
El denominador cuenta registros elegibles de Identity, no correos únicos:
en Acme son siete registros para seis personas; Eve aporta dos desconocidos.
El cálculo de dispositivos sólo incluye personas con un registro Identity y
un usuario Netskope. El detalle de Eve no atribuye equipos, aunque la ficha
Netskope conserva su observación propia de un equipo. Una población vacía o fuente no vigente no produce 0 %.

El laboratorio inicializa una captura completa y sintética de
Personas/cobertura; no expone una API para fabricar corridas nuevas de ese
escenario. Las ramas de evidencia incompleta se verifican en el núcleo y las
lecturas de corridas persistidas; la sincronización de inventario tiene su
propio recorrido más abajo. La evidencia envejece de verdad entre lecturas y
reinicios.

La demo TS cuenta dispositivos de personas ambiguas en su agregado. En Acme,
el agregado Python/Go produce 3 cubiertos / 7 registros (42,86 %)
y 4 equipos recientes / 5 evaluables; el agregado de la demo incluye
el dispositivo de Eve. No se cambió la demo ni se la usa para sustituir el
contrato Python. La integración de la UI deberá hacer visible ese denominador.

## Persistencia y verificación

SQLite en archivo descartable: migración inicial transaccional e idempotente,
seed que rechaza reemplazo, reapertura conservando datos y fecha, lecturas
concurrentes, formato de respuestas HTTP y rechazos de credencial/escritura.
CLI: directorio nuevo, token/archivo privados y rechazo de escucha no loopback.
El fixture embebido se compara con el escenario compartido de web/.

`npm run verify:go` ejecuta vet, tests con detector de carreras y build.
Las pruebas trasladan casos explícitos del contrato Python; no se afirma una
comparación diferencial de todos los endpoints ni paridad completa con v1.

La UI real ya navega Personas/cobertura contra Go en el gate verify:go:web,
sin API simulada. Incluye preferencias persistidas y auditadas, ausencia de
consultas PAM y reinicio sin rejuvenecer la evidencia. No prueba el ingreso
interactivo; el harness inyecta la credencial sintética de laboratorio.

Las observaciones se persisten por conexión, colección e ID externo. Las
respuestas HTTP ya no leen el snapshot preunido. Se conservan sólo campos
permitidos del escenario; las credenciales secundarias se guardan por hash.
Las consultas hacen join con concesiones dentro de un mismo snapshot SQLite.
Cobertura requiere ambas fuentes y la regla explícita del par seleccionado;
no tener permiso produce 403, sin transformar una fuente oculta en ausencia.
La ficha de una sola fuente omite las herramientas no legibles. Las preferencias
y su auditoría pertenecen a cada principal.

Las pruebas incluyen IDs iguales en dos conexiones del mismo proveedor y en
dos colecciones, fuentes parciales/ausentes, pares sin regla, búsqueda/ficha/
porcentajes sin filtraciones, revocación inmediata y rollback de concesiones
o revocación ante una auditoría fallida. Queda por integrar la identidad real,
la administración de conexiones/reglas y las observaciones remotas de
proveedores. El primer tramo de sincronización de laboratorio se detalla
abajo; conectores reales, worker asíncrono, backup/restore y distribución
siguen pendientes en [el tracker](TO_IMPLEMENT.md).

## Primera sincronización de inventario — 16/11

Alcance acotado: **una colección normalizada por conexión**. El conector
`http-inventory-v1` lee `users` o `devices` de un servidor HTTP local
sintético y no acredita ningún fabricante. El conector
`cyberark-pam-safes-v1` implementa los contratos documentados `/API/Safes`,
`/API/Safes/{safeName}/Members`, `/API/Accounts`, `/API/Platforms`,
`/API/PSM/Servers` y `/API/PSM/Connectors` de CyberArk PAM contra un fixture
HTTP loopback, más el cruce `platform_psm_links`: traduce sólo las allowlists
de Safe, miembro, cuenta no secreta, plataforma, servidor PSM, conector PSM y
vínculo resuelto, y no lee contraseñas, secretos, propiedades ni el payload
completo. Tampoco envía credenciales ni usa TLS en este tramo; un tenant real
requiere el probe autorizado y el perfil de transporte que siguen pendientes. La unidad
persistida sigue siendo **conexión + colección + ID externo**, con corrida de
procedencia. `source_system` se deriva del conector: `netskope` para el
laboratorio normalizado y `cyberark` para Safes PAM.

### Rutas y contrato HTTP

| Recorrido | Ruta | Implementación y límite |
|---|---|---|
| Iniciar corrida | `POST /api/ingestion/schedules/{connection}/runs` | Exige lectura y `can_run` de esa conexión. Persiste `queued` y auditoría `sync_queued` antes de la primera llamada; el worker reclama con lease y audita `sync_start`, y termina en `completed`, `partial` o `failed` con `sync_complete`. 202, 400, 403, 404 o 409 si hay una corrida `running` con lease vigente. Una corrida `queued` o con lease vencido se reencola/retoma con el mismo pedido. |
| Sincronización completa | `POST /api/ingestion/all/runs?scope=all|cyberark` | Encola una corrida por cada conexión legible con `can_run`; omite las que ya tienen una corrida activa. `scope=cyberark` puede devolver una lista vacía sin error. 202; 403 si el principal no puede ejecutar ninguna conexión. |
| Historial | `GET /api/ingestion/runs?limit=` | Sólo corridas de conexiones legibles por el principal. No revela totales ni IDs de conexiones ocultas. |
| Detalle | `GET /api/ingestion/runs/{run_id}` | 404 si la corrida no existe o pertenece a una conexión no autorizada. |
| Capas | `GET /api/ingestion/runs/{run_id}/stages` | Una capa `extract` derivada de la corrida; no hay tabla de etapas ni telemetría por capa. |
| Etapas por fuente | `GET /api/ingestion/sources/{source}/stages/latest` y `/history?runs=` | Frescura e historial derivados de las corridas sync y filtrados por `source_system` (`netskope` o `cyberark`). Sin payloads. |
| Agenda | `GET /api/ingestion/schedules` | Estado por conexión legible: activa, intervalo, último éxito, último intento, último estado, fallos consecutivos, frescura y próximo intento. El último éxito incluye `completed` y `partial` como refresh, igual que la referencia Python. |
| Cambiar agenda | `PUT /api/ingestion/schedules/{connection}` | Intervalo `0` (deshabilitado) o `5–525600` minutos; exige lectura y `can_run`; audita `sync_schedule`. |
| Registros | `GET /api/ingestion/connections/{connection}/records?status=&collection=&limit=` | Observaciones activas o retiradas con `first_run_id`, `last_run_id`, `scope`, `generation`, `retired_at` y `retired_by_run_id`. |
| Agenda global | `GET /api/ingestion/schedule` | Respuesta informativa deshabilitada; la agenda real es por conexión. |

### Reglas trasladadas de Python

- **Cola y auditoría antes de la red.** `sync_queue.enqueue_sync_run` inserta
  `queued` y `sync_queued` en una transacción; `claim_next_sync_run` marca
  `running`, incrementa `attempt_count`, fija lease de 15 minutos y audita
  `sync_start`; `requeue_expired_sync_runs` devuelve a `queued` con
  `sync_requeued`. Go reproduce esos estados y auditorías. En Python,
  `test_enqueue_persists_audited_queued_job` y
  `test_claim_transitions_only_its_source_and_audits_start` fijan el contrato;
  en Go, `TestSyncQueueAndStartAuditsAreAtomic` prueba el rollback de ambas
  mutaciones.
- **Cierre y estados.** `ingestion.finish_ingestion_run` persiste conteos,
  `error_details` y `sync_complete` en la misma transacción; una capa opcional
  fallida produce `partial`, no `completed`. Go usa `completed`, `partial` y
  `failed`, `warnings_json`, `error_code`/`error_detail` saneados y una única
  auditoría `sync_complete` por corrida. Evidencia Python:
  `test_completion_audit_records_counts_in_the_same_transaction`,
  `test_failed_optional_stage_finishes_partial_with_warning` y
  `test_a_run_that_left_work_for_the_next_cycle_finishes_partial`; evidencia
  Go: `TestSyncCompletePublishesAndRetiresOnlyComparableAbsences`,
  `TestSyncPartialPublishesButNeverRetiresOrReplacesBaseline` y
  `TestSyncFailureLeavesObservationsAndAuditAtomic`.
- **Identidad y duplicados.** La clave Python es
  `(source_system, object_type, source_id)`; `deduplicate_snapshot` elimina
  duplicados idénticos y rechaza atributos contradictorios antes de escribir.
  Go usa `(connection_id, collection, external_id)` con la misma regla; las
  pruebas Go son `TestDeduplicateIdenticalAndConflicting` y
  `TestSyncConflictingDuplicateFailsWithoutPublication`. Un conflicto falla la
  corrida completa y no publica nada.
- **Bajas sólo con evidencia completa.** `cmdb_persistence.reconcile_objects`
  exige `source_run_id` y sólo retira tipos declarados para una lectura
  completa; una capa opcional fallida no se pasa a reconciliación. Go exige
  una corrida `completed` del **mismo alcance y generación** que la línea base;
  `partial` y `failed` nunca retiran ni reemplazan la línea base. Una corrida
  completa con alcance nuevo crea una línea base nueva y no inventa bajas
  históricas (prueba Go `TestSyncCompletePublishesAndRetiresOnlyComparableAbsences`).
- **Parcialidad útil.** Una corrida parcial publica las páginas recibidas y
  actualiza sus observaciones, pero no retira ausentes ni reemplaza la línea
  base; Python actualiza las capas leídas y conserva las capas cuyo tipo no se
  reconcilia. Go agrega además `warnings_json` y conserva el `last_run_id` de
  los registros no vistos (prueba `TestSyncPartialPublishesButNeverRetiresOrReplacesBaseline`).
- **Cero confirmado.** Una corrida `completed` comparable con cero objetos
  retira los activos de ese alcance; un error, 403, timeout o página faltante
  no se convierte en cero. Go lo cubre con
  `TestSyncZeroConfirmedAndRepeatedSnapshotDoNotDuplicate`.
- **Decisiones humanas.** Python conserva `data_quality_status` no pendiente
  en el upsert y mantiene las revisiones en tablas separadas; no hay una prueba
  directa del `CASE`, lo que queda anotado como diferencia de evidencia. Go
  crea `lab_sync_reviews`, una tabla que el sync nunca escribe; la prueba
  `TestSyncReviewSurvivesObservationRefresh` demuestra que un cambio de
  atributos no pisa la revisión. La API HTTP de revisión todavía no existe.
- **Idempotencia y repetición.** `enqueue_sync_run` reutiliza
  `(triggered_by, idempotency_key)` sin nueva auditoría. Go usa
  `(requested_by, connection_id, idempotency_key)` y además un índice único
  parcial de una corrida `queued`/`running` por conexión. Repetir el mismo
  snapshot no duplica observaciones; `TestSyncRunsAreIdempotentAndOneActivePerConnection`,
  `TestSyncIdempotencyIsScopedByConnection` y
  `TestSyncZeroConfirmedAndRepeatedSnapshotDoNotDuplicate` lo verifican.

### Paginación, errores y límites

El adaptador `httpinventory` pide
`GET {base}/v1/collections/{collection}?limit=N&cursor=...` y exige
`{"objects":[...],"next_cursor":""}` con ambos campos presentes; `objects:[]`
y `next_cursor:""` explícitos son la única forma de acreditar una colección
vacía. `objects` nulo, ausente o `next_cursor` nulo/ausente se rechazan. El
cursor vacío significa fin de colección. Límites: página 1–500, `max_pages`
1–1000, `max_records` 1–100000, cuerpo 1 MiB, timeout 10 s, sin redirects, sin
cookies ni credenciales, `Content-Type` JSON estricto y allowlist de atributos
por colección. Un cursor repetido, página demasiado grande, cuerpo demasiado
grande, JSON inválido o duplicado contradictorio fallan la corrida completa;
un 403/429/5xx/timeout después de una página produce `partial` con lo ya
recibido. Códigos seguros (no exhaustivos): `http_status_<code>`, `timeout`,
`request_failed`, `redirect`, `invalid_json`, `invalid_content_type`,
`body_too_large`, `page_too_large`, `invalid_cursor`, `cursor_repeated`,
`invalid_object`, `duplicate_conflict`, `canceled`, `configuration`,
`configuration_changed`, `authorization_revoked`. Python
tiene reintentos, `Retry-After`, paginación por offsets y límites específicos
de Netskope; Go deliberadamente no implementa esos detalles de proveedor.

El adaptador `cyberarkpam` implementa el contrato PAM verificado en DS Hub:
`GET /API/Safes?limit=N` devuelve `{"value":[...],"count":N,"nextLink":...}`.
Sigue `nextLink` sólo si es del mismo origen y permanece en `/API/Safes`; un
destino externo, otro path, fragmento o usuario se rechaza con
`foreign_cursor`. `count` es el total esperado: si no coincide con lo leído,
o cambia entre páginas, la corrida falla y no publica; el runner ahora lleva
`Page.Expected` con esa comprobación. La allowlist de Safe es `safe_name`,
`safe_url_id`, `location`, `creator_name`, `olac_enabled`,
`versions_retention`, `days_retention`, `auto_purge_enabled`, `creation_time`,
`last_modification_time`, `description`, `managing_cpm` y `expired_member`; el
ID externo es `safeNumber`. Los epochs se convierten a RFC3339 y los campos
fuera de la allowlist se descartan. Se excluyen cuentas, secretos y el payload
completo. Un 401 produce `authentication_failed`, un 403
`permission_denied`; una página 2 con error conserva lo leído como `partial` y
no retira Safes ausentes.

La colección `safe_members` enumera primero `/API/Safes` completo y pagina
`/API/Safes/{safeName}/Members`. El cursor opaco lleva el Safe actual y el
`nextLink`; se rechaza un cursor que cambie de Safe u origen. La allowlist es
`safeNumber`, `safeName`, `memberId`, `memberName`, `memberType`,
`membershipExpirationDate`, `isExpiredMembershipEnable`, `isPredefinedUser`,
`isReadOnly` y `permissions`; el ID externo es `safeNumber:memberId`. Los
permisos se guardan como objeto JSON acotado. El adaptador valida el total por
Safe al terminar su lista y el total agregado al cerrar la colección: una
lista corta o un total cambiante falla sin publicar. Un error de página
posterior deja `partial` y no retira miembros.

La colección `accounts` pagina `/API/Accounts` y resuelve el Safe padre por
nombre contra la enumeración acotada de Safes. La allowlist es `account_id`,
`safe_number`/`safe_name`, `account_name`, `user_name`, `address`,
`platform_id`, `account_type`, `category`, `automatic_management_enabled`,
`management_status` y `secret_modified_at`; el ID externo es
`safeNumber:accountId`. `secretManagement` se reduce a esos tres campos y se
excluyen contraseñas, `platformAccountProperties`, `manualManagementReason`,
`lastModifiedBy`, `secretType` y el payload. Una cuenta sin Safe visible o sin
ID se omite; el adaptador agrega el aviso `skipped_accounts` y el runner
termina `partial`, publica lo leído y no retira cuentas ni reemplaza la línea
base. Una lista completa con total inconsistente falla sin publicar. La
cadena `users → roles → safes → platforms → psm` y la resolución de nombres
de plataforma quedan para tramos posteriores.

La colección `platforms` lee `/API/Platforms`, que el contrato verificado
entrega `{Platforms:[...], Total:N}` en una sola respuesta. El adaptador
valida `Total` contra la lista y sirve páginas locales acotadas con cursor
propio, sin inventar parámetros de paginación. La allowlist es `general.id`
(ID externo), `general.name`, `general.active`, `general.platformBaseID`,
`general.platformType`, `general.systemType` y
`sessionManagement.PSMServerID`. Se excluyen `properties`, políticas, reglas
de contraseña y el payload. `psm_server_id` queda como referencia textual: no
se cruza todavía con un catálogo PSM ni crea una arista. El detalle
`/API/Platforms/{id}` y `Details.AllowedSafes` quedan fuera de este tramo.

Los catálogos `psm_servers` y `psm_connectors` leen
`/API/PSM/Servers?limit=1000` y `/API/PSM/Connectors?limit=1000`, validan
`Total` y sirven páginas locales acotadas. La allowlist de servidor es `ID`,
`Name` (fallback al ID) y `Address`; la de conector es `ID` y `DisplayName`
(fallback al ID). Se excluyen propiedades, capacidad, salud y el payload.

`platform_psm_links` cruza `sessionManagement.PSMServerID` contra
`psm_servers` en una corrida. `exact` coincide literalmente; `casefold` tiene
un único ID que difiere sólo en mayúsculas; `ambiguous` tiene dos o más IDs
distintos que difieren sólo en mayúsculas y se publica sin objetivo;
`missing` no tiene candidato y conserva el ID crudo. El ID externo es
`platformID:referencePSMID`; la allowlist es `platform_id`, `platform_name`,
`reference_psm_id`, `psm_server_id`, `psm_server_name` y `resolution`. Una fila
`ambiguous` no es una arista, es evidencia de la omisión; una `missing` no
inventa un servidor del catálogo. Sólo una corrida completa retira vínculos
cuya referencia desapareció. El cruce no usa nombres, prefijos ni similitud.

### Autorización y auditoría

La lectura usa `lab_grants`; iniciar una corrida exige además
`lab_sync_grants.can_run=1`. Un administrador no evade esas concesiones. Las
listas se filtran antes de responder; una corrida oculta da 404 y una conexión
oculta en registros da 403. La publicación revalida en su transacción que la
fuente siga habilitada con la misma URL base, alcance y generación del
enqueue, y que el solicitante conserve lectura y `can_run`; si cambió, la
corrida falla con `configuration_changed` o `authorization_revoked` y no
publica. `auth/status` anuncia `ingestion:run` cuando el principal tiene al
menos una conexión sync legible y habilitada con `can_run`; las conexiones
sync siguen sin conceder `netskope:read`. Se auditan `sync_configure`,
`sync_grant`, `sync_schedule`, `sync_queued`, `sync_start`, `sync_complete`,
`sync_requeued` y `sync_review` en la misma transacción que su cambio. No hay
logs operativos ni tabla de etapas; esa telemetría sigue pendiente.

### Diferencias y límites vigentes

- SQLite de una sola instancia: sin advisory locks de PostgreSQL. El claim usa
  `UPDATE ... WHERE status='queued'` + `RowsAffected`; el índice único parcial
  impide dos corridas activas por conexión. El fencing usa `worker_id` y
  `attempt_count`: un intento viejo no puede renovar el lease ni publicar
  después de un requeue o de un nuevo claim. El worker corre en el proceso
  serve, reclama de a una corrida, renueva heartbeat/lease cada 30 s y
  reencola vencidas; `RequeueExpiredSyncRuns` también queda disponible de
  forma explícita.
- La agenda es por conexión: `interval_minutes=0` deshabilita; un valor
  positivo encola cuando pasa el intervalo desde el último intento. Un fallo
  se reintenta en el siguiente intervalo; todavía no hay backoff exponencial,
  política por fuente ni prioridad. El worker es único, no un pool.
- Una colección por conexión y una corrida activa por conexión. Python usa
  `task_key` y permite tareas hermanas de una fuente; esa separación queda para
  el próximo tramo.
- Sin conector real, sin secretos, sin TLS, sin probe ni administración de
  conexiones por API. Los dos adaptadores son de lectura y sólo aceptan un
  origen HTTP numérico de loopback; el de Safes PAM usa el contrato CyberArk
  contra un fixture, no contra un tenant.
- La UI de sync ya inicia, consulta y agenda con los contratos anteriores:
  `ingestion:run` se anuncia, `/all/runs` encola, la agenda se edita y las
  etapas por fuente se consultan. `scope=cyberark` aísla las conexiones PAM.
  Siguen sin migrar las revisiones humanas por HTTP, la administración de
  conexiones, el probe y los proveedores reales. La agenda usa timestamps
  RFC3339 de segundo; el orden de dos corridas del mismo segundo no es
  significativo.
- Sin relaciones entre objetos ni grafo CMDB; esta primera entrega sólo
  persiste observaciones de una colección y sus bajas por ausencia. La
  publicación carga las observaciones existentes de esa conexión/colección en
  memoria para calcular altas, cambios y retiros; no hay poda de retirados ni
  reconciliación por lotes, así que el costo crece con el histórico. El
  endpoint de registros usa `LIMIT` sin cursor; una colección mayor al límite
  no se recorre completa. Una conexión sync puede seleccionarse explícitamente
  en Personas/cobertura sólo si existe una regla de par; el laboratorio no crea
  esas reglas.

### Verificación

**Estado del tramo:** diseño y código en `server/`; pruebas automatizadas en
`verify:go` y regresión de UI/gateway; sin despliegue, proveedor real ni
aceptación de una instancia real de Ozal.

`npm run verify:go` ejecuta vet, tests con detector de carreras y build. Las
pruebas nuevas incluyen adapter con `httptest`, runner de paginación, dos
conexiones con el mismo ID externo, página 2 fallida, primera página fallida,
duplicados idénticos/contradictorios, cursor repetido, cero confirmado,
repetición, idempotencia por conexión, lease vencido y fencing por intento,
revalidación de fuente/permisos antes de publicar, worker y heartbeat,
scheduler por intervalo, agenda editable, `/all/runs`, etapas por fuente,
revisión humana y permisos insuficientes. El adaptador PAM agrega paginación
`value/count/nextLink`, allowlist/epochs, total inconsistente, nextLink
externo/repetido, 401/403/429/5xx, página 2 parcial, publicación, retiro sólo
completo, aislamiento `cyberark`/`netskope` y permisos. La colección de
miembros agrega enumeración de Safes, paginación por Safe, cursor ligado al
Safe, total por Safe y agregado, allowlist de miembro, publicación, parcialidad
y retiro sólo completo. La colección de cuentas agrega paginación
`/API/Accounts`, resolución de Safe por nombre, allowlist sin secretos,
`secretManagement` saneado, cuentas omitidas con aviso y parcialidad sin
retiro. La colección de plataformas agrega lista `{Platforms,Total}`,
paginación local, allowlist, total inconsistente, página acotada parcial y
permisos. Los catálogos PSM agregan `{PSMServers,Total}` y
`{PSMConnectors,Total}`, allowlist, total inconsistente, página acotada
parcial y permisos. El cruce agrega exact/casefold/missing/ambiguous,
retiro sólo completo, parcial sin retiro y permisos. `verify:go:web` recorre
además la UI real contra ocho conexiones (usuarios, Safes PAM, miembros,
cuentas, plataformas, servidores PSM, conectores PSM y vínculos
plataforma↔PSM): inicia sync desde el menú, espera las corridas, consulta
registros sin secretos, ejecuta `/all/runs` por alcance y actualiza la agenda;
`verify:go:gateway` conserva la regresión del receptor Ozal. `go test -race
./internal/probe` y `go test ./cmd/custodia` cubren el transporte TLS de
lectura y el comando `probe-pam` con bearer, OAuth client-credentials, mTLS y
referencias `env:`/`file:`/`secret:`, CA temporal, límites, redirects
rechazados, JSON saneado y códigos de salida `0/1/2`; no contactan un tenant
real. Ninguno prueba un proveedor real ni una instalación desplegada.
