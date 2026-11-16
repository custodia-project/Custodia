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

Alcance acotado: **una colección normalizada por conexión** (`users` o
`devices`) leída de un servidor HTTP local sintético. No es un adaptador de
Netskope, CyberArk ni de ningún fabricante: el contrato de laboratorio es
`http-inventory-v1`, sólo acepta HTTP numérico loopback y no envía
credenciales. La unidad persistida sigue siendo **conexión + colección + ID
externo**, con corrida de procedencia. El campo `source_system` se reporta
como `netskope` sólo para encajar en el contrato tipado que ya consume la UI;
no acredita un adaptador de ese fabricante.

### Rutas y contrato HTTP

| Recorrido | Ruta | Implementación y límite |
|---|---|---|
| Iniciar corrida | `POST /api/ingestion/schedules/{connection}/runs` | Exige lectura y `can_run` de esa conexión. Persiste `queued` y auditoría `sync_queued` antes de la primera llamada, reclama con lease y audita `sync_start`; el laboratorio ejecuta la extracción en la misma petición y termina en `completed`, `partial` o `failed` con `sync_complete`. 202, 400, 403, 404 o 409 si hay una corrida `running` con lease vigente. Una corrida `queued` o con lease vencido se reencola/retoma en el mismo pedido. |
| Historial | `GET /api/ingestion/runs?limit=` | Sólo corridas de conexiones legibles por el principal. No revela totales ni IDs de conexiones ocultas. |
| Detalle | `GET /api/ingestion/runs/{run_id}` | 404 si la corrida no existe o pertenece a una conexión no autorizada. |
| Capas | `GET /api/ingestion/runs/{run_id}/stages` | Una capa `extract` derivada de la corrida; no hay tabla de etapas ni telemetría por capa. |
| Agenda | `GET /api/ingestion/schedules` | Estado por conexión legible: activa, último éxito, último intento, último estado y fallos consecutivos. El último éxito incluye `completed` y `partial` como refresh, igual que la referencia Python; `interval_minutes=0` y `freshness=unknown` porque no hay scheduler. |
| Registros | `GET /api/ingestion/connections/{connection}/records?status=&collection=&limit=` | Observaciones activas o retiradas con `first_run_id`, `last_run_id`, `scope`, `generation`, `retired_at` y `retired_by_run_id`. |
| Agenda global | `GET /api/ingestion/schedule` | Devuelve deshabilitada; cambiar agenda y `/api/ingestion/all/runs` siguen sin migrar. |

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

### Autorización y auditoría

La lectura usa `lab_grants`; iniciar una corrida exige además
`lab_sync_grants.can_run=1`. Un administrador no evade esas concesiones. Las
listas se filtran antes de responder; una corrida oculta da 404 y una conexión
oculta en registros da 403. La publicación revalida en su transacción que la
fuente siga habilitada con la misma URL base, alcance y generación del
enqueue, y que el solicitante conserve lectura y `can_run`; si cambió, la
corrida falla con `configuration_changed` o `authorization_revoked` y no
publica. `auth/status` no convierte una conexión de laboratorio en
`netskope:read` ni anuncia `ingestion:run`, porque la UI todavía no ofrece el
recorrido de inicio. Se auditan `sync_configure`, `sync_grant`, `sync_queued`,
`sync_start`, `sync_complete`, `sync_requeued` y `sync_review` en la misma
transacción que su cambio. No hay logs operativos ni tabla de etapas; esa
telemetría sigue pendiente.

### Diferencias y límites vigentes

- SQLite de una sola instancia: sin advisory locks de PostgreSQL. El claim usa
  `UPDATE ... WHERE status='queued'` + `RowsAffected`; el índice único parcial
  impide dos corridas activas por conexión. El fencing usa `worker_id` y
  `attempt_count`: un intento viejo no puede renovar el lease ni publicar
  después de un requeue o de un nuevo claim. `RequeueExpiredSyncRuns` es
  explícito; el POST de la conexión usa la variante acotada a esa conexión,
  audita al llamador y retoma una corrida `queued` o con lease vencido. No hay
  coordinador ni heartbeat en segundo plano.
- El laboratorio ejecuta la extracción en la petición HTTP. La cola y el lease
  son durables, pero un worker asíncrono, reintentos con backoff y recuperación
  automática después de un crash no están implementados.
- Una colección por conexión y una corrida activa por conexión. Python usa
  `task_key` y permite tareas hermanas de una fuente; esa separación queda para
  el próximo tramo.
- Sin conector real, sin secretos, sin TLS, sin probe ni administración de
  conexiones por API. El adaptador es sólo para servidor local sintético.
- Sin UI de sincronización: están migrados `GET /api/ingestion/runs`,
  `GET /api/ingestion/runs/{id}`, `GET /api/ingestion/runs/{id}/stages`,
  `GET /api/ingestion/schedules`, `GET /api/ingestion/schedule` (deshabilitada)
  y `POST /api/ingestion/schedules/{id}/runs`, además de
  `GET /api/ingestion/connections/{id}/records`. Siguen sin migrar
  `GET /api/ingestion/sources/{source}/stages/history`,
  `PUT /api/ingestion/schedules/{id}`, `/api/ingestion/all/runs` y las
  revisiones por HTTP. No se expone `ingestion:run` en `auth/status`, así que
  los controles de sync de la UI permanecen ocultos. La agenda usa timestamps
  RFC3339 de segundo y no hay scheduler; el orden de dos corridas del mismo
  segundo no es significativo.
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
revalidación de fuente/permisos antes de publicar, revisión humana y permisos
insuficientes. `verify:go:web` y `verify:go:gateway` se ejecutan como
regresión de la UI real y del receptor Ozal; no prueban una fuente real ni una
instalación desplegada.
