---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Conectores: una experiencia común, contratos específicos

**Estado:** contrato de producto. El primer adaptador de laboratorio
(`http-inventory-v1`) ya está implementado contra un servidor HTTP local
sintético y probado con SQLite descartable. No hay conector real, probe ni API
administrativa de conexiones disponible.
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

Capacidades de inventario, métricas agregadas, detalle sensible y catálogo
en vivo son distintas. Una conexión puede servir sólo algunas. Una vista
sólo se ofrece si su capacidad y permiso están disponibles. Un catálogo
consultado en vivo no se presenta como snapshot histórico.

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
host con `add-http-lab`; las cuentas y secretos reales, la configuración por
API y el probe de proveedor quedan fuera de este tramo.
