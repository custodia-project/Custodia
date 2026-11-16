---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Primer recorrido Go: Personas y cobertura

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

El laboratorio inicializa una captura completa y sintética; no expone una API
para fabricar corridas nuevas. Las ramas de evidencia incompleta se verifican en el núcleo y las lecturas
de corridas persistidas; falta verificar la futura ingesta y sus reintentos.
La evidencia envejece de verdad entre lecturas y reinicios.

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
la administración de conexiones/reglas, sincronización y observaciones remotas.
Conectores, sincronización durable, auditoría de inventario, backup/restore y distribución
siguen pendientes en [el tracker](TO_IMPLEMENT.md).
