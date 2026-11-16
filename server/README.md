# Backend Go de Custodia

Primer recorrido sintético de búsqueda, ficha Identity/Netskope y cobertura.
SQLite local es la autoridad del laboratorio. Usa Go 1.26+ y modernc.org/sqlite
v1.60.1 (MIT, sin CGO), fijado con sus dependencias en go.mod/go.sum.
No hay conectores reales, ingesta real de proveedores ni exportación. El modo serve
incorpora identidad firmada de Ozal; su integración desplegada sigue pendiente.
Las credenciales siguen siendo sintéticas y locales. La autorización ya se
resuelve desde principales habilitados y concesiones de lectura por conexión
en SQLite; una credencial no concede acceso universal a las fuentes.
El primer adaptador de sincronización de lectura es `http-inventory-v1`, sólo
para un servidor HTTP sintético en loopback; no acredita soporte de Netskope,
CyberArk ni otro fabricante.

Desde la raíz:

```sh
npm run verify:go
cd server
go build -o /tmp/custodia ./cmd/custodia
/tmp/custodia init-lab --dir /tmp/custodia-lab
/tmp/custodia serve-lab --dir /tmp/custodia-lab --web-dir /ruta/al/build/web
```

La inicialización rechaza una carpeta existente, crea archivos privados y
aplica la migración inicial. No abre ni modifica bases anteriores. Sirve sólo
en una dirección numérica loopback y exige el token del archivo lab.token en
Authorization: Bearer. No imprimir ni compartir ese archivo. La identidad por
headers de proxy no se acepta. No hay fallback a este modo desde un modo real.

Rutas GET:

- `/api/auth/status`: principal sintético y capacidades derivadas de sus concesiones.
- `/api/connections`: únicamente conexiones que ese principal puede leer.
- `/api/chains/person/search?q=Alice`: búsqueda.
- `/api/chains/person?email=alice.morgan%40acme.example`: ficha de dos fuentes.
- `/api/chains/person/coverage`: resumen.
- `/api/chains/person/coverage/detail?email=alice.morgan%40acme.example`: detalle.

El backend sirve opcionalmente el build real de web/ con --web-dir. La UI de
Personas/cobertura ya se verifica contra Go y SQLite en navegador, sin mock.
El harness inyecta la credencial de laboratorio por header; todavía falta
la aceptación del ingreso contra una instancia real de Ozal.

Rutas auxiliares: preferencias propias GET/PUT en /api/workspaces/preferences,
con escritura y auditoría transaccional; /api/events sigue vacía porque el
feed de actividad no está migrado; las corridas y auditorías de sync se
consultan en /api/ingestion. Las demás rutas responden 404 y las otras
escrituras 405. No se fabrican métricas PAM para satisfacer consultas de la
interfaz. Personas no solicita su dashboard.

Las bases inicializadas con el primer laboratorio requieren ejecutar
`custodia migrate-lab --dir /ruta/al/laboratorio` antes de servir. La migración
aditiva no cambia la captura existente y el arranque rechaza versiones
incompatibles; el servidor no aplica migraciones silenciosas.

```sh
npm run verify:go:web
```

Este gate compila la UI en modo real (VITE_DEMO=0) y el binario sin CGO,
crea una base temporal, prueba Alice/Bob/Eve, el denominador Python, el
reinicio con evidencia y preferencias conservadas, usuarios de alcance distinto,
rechazo de cruces sin permiso y revocación con el proceso en ejecución. Necesita Chromium de Playwright. El token no se imprime ni se guarda
en el frontend y los procesos temporales se detienen al terminar.

## Alcances de laboratorio

init-lab crea un principal sintético con lectura de acme-identity y
acme-netskope. Las conexiones nuevas no se conceden automáticamente.
El operador del host puede emitir una credencial distinta con alcance acotado:

```sh
/tmp/custodia issue-lab --dir /tmp/custodia-lab --subject identity-reader --connections acme-identity --out /tmp/custodia-identity.token
/tmp/custodia revoke-lab --dir /tmp/custodia-lab --subject identity-reader
```

La credencial se genera aleatoriamente y sólo se escribe en un archivo nuevo
0600. No se imprime. La base guarda su hash, el par emisor/sujeto sintético,
las concesiones y la auditoría de emisión/revocación. Emitir de nuevo sobre
un sujeto existente falla; no modifica sus permisos. Revocar se aplica en la
siguiente petición, también para el principal inicial: no hay bypass por token.
Estas operaciones son administrativas locales, no endpoints públicos.

El usuario de una sola fuente obtiene búsquedas y fichas de esa fuente. Las
rutas de cobertura y sus denominadores requieren permiso de ambas conexiones
y una regla registrada para ese par. Un alcance insuficiente da 403 para un
correo existente o inexistente. Parámetros identity_connection y
netskope_connection seleccionan conexiones explícitas; no conceden acceso.
No hay todavía un selector de conexiones múltiples en la interfaz.

## Observaciones y migración

La migración 3 agrega conexiones, corridas, observaciones y concesiones.
Cada observación tiene ID interno y clave única conexión + colección + ID
externo opaco. Los equipos referencian el usuario de su propia conexión.
El correo exacto relaciona candidatos sólo en el par de fuentes aprobado;
un vínculo ambiguo no se convierte en cobertura. Las lecturas hacen join de
concesiones y observaciones dentro de una misma transacción de lectura.

La migración de un laboratorio anterior conserva sus fechas, preferencias y
snapshot archivado. El snapshot ya no alimenta las respuestas HTTP. Los
laboratorios antiguos que descartaron el ID externo de sus equipos reciben
IDs locales explícitos lab-legacy-device-...; no se presentan como IDs de APIs
reales. Las nuevas inicializaciones conservan el deviceId sintético original.
Las migraciones aplicadas anteriores se mantienen y las bases futuras se
rechazan; el servidor no migra ni renueva evidencia al arrancar.

Las fechas se trasladan una sola vez al inicializar. Al reiniciar o leer,
la evidencia envejece: después de 24 horas produce unknown. Completitud y
frescura se evalúan por conexión. Las preferencias y su auditoría se aíslan
por principal, con revalidación de habilitación dentro de la transacción.

La clasificación y los límites de frescura siguen la referencia Python en
f6a7acc, servicio person_client_coverage y sus pruebas de contrato. Incluye
ambigüedad, parcialidad, ausencia y fuentes vencidas. Los totales de dispositivos
excluyen identidades ambiguas como en Python. El denominador de población
cuenta siete registros Identity de seis personas; Eve aporta dos registros
desconocidos. La demo TS tiene otro agregado. La ficha de la herramienta
conserva los dispositivos observados; el detalle de cobertura sólo los
atribuye si ambos vínculos son únicos. Los porcentajes y edades usan el
redondeo de la referencia Python, incluidos los límites de mitad decimal.
El seed procede del fixture público web/src/demo/model/company.json, con una
copia embebida comprobada por pruebas. No usa APIs o datos privados.

La sincronización HTTP de laboratorio registra fuentes con límites acotados y
publica observaciones con procedencia; la aceptación de una instancia real de
Ozal y recorridos de ingesta en navegador siguen pendientes. OIDC genérico
queda pendiente.
La distribución final deberá incluir frontend, instrucciones de configuración,
versionado y backup/restore consistente de SQLite; no se anuncia todavía una
versión descargable para producción.

## Sincronización HTTP de laboratorio

El operador del host registra una fuente read-only contra un servidor HTTP
numérico de loopback, con colección, scope y generación explícitos:

```sh
/tmp/custodia add-http-lab --dir /tmp/custodia-lab --connection lab-http-a \
  --url http://127.0.0.1:18080 --collection users --scope scope-a \
  --grant synthetic-lab
```

El registro rechaza orígenes que no sean http numérico de loopback,
colecciones no soportadas (users/devices), scope ausente y conexiones
duplicadas, y audita configuración y concesiones en la misma transacción.
Las concesiones distinguen lectura de arranque de corridas (can_run) y sólo
se otorgan sobre principales habilitados existentes; no hay API administrativa
pública todavía. Para un lector existente:

```sh
/tmp/custodia grant-sync-lab --dir /tmp/custodia-lab \
  --connection lab-http-a --subject reader [--can-run=false]
```

La lectura se concede siempre; `--can-run=false` deja al lector sin permiso
para iniciar corridas. Ambas operaciones quedan auditadas en el host y no
existe todavía una pantalla de administración.

Rutas de /api/ingestion: corridas GET con límite acotado, corrida por ID y sus
etapas, agendas con último éxito y fallos consecutivos, registros por conexión
con filtros collection/status, y POST schedules/{id}/runs con cuerpo vacío e
Idempotency-Key acotada para encolar una corrida (409 si ya hay una running con
lease vigente; una queued o vencida se retoma, 403 sin can_run, 404 sin revelar
conexiones ocultas). La corrida se ejecuta en el mismo pedido pero queda
registrada como durable antes de la primera llamada externa.

La publicación es transaccional y auditada. Una corrida completed con el
mismo scope y generación que la línea base puede dar de baja ausencias; una
parcial actualiza observaciones pero nunca retira ni reemplaza la línea base;
una fallida no publica observaciones ni reemplaza la línea base, aunque
registra su estado y `sync_complete`. El fencing usa worker, lease y
`attempt_count`: un intento viejo no puede renovar el lease ni publicar. El
vencimiento se reencola de forma explícita, acotada a la conexión y auditada
con el actor que la solicita. Las decisiones humanas viven en una tabla
separada que la sincronización no escribe, sin endpoint todavía. Antes de
publicar se revalida la fuente (URL, scope, generación, habilitación) y las
concesiones del solicitante; un cambio falla sin escribir. No hay conector
real, credenciales de proveedor ni agenda programada: la frescura se reporta,
no se infiere. El campo `source_system` se reporta como `netskope` sólo por el
contrato tipado de la UI; no acredita un conector de ese fabricante.

El servidor local responde
`GET {base}/v1/collections/{collection}?limit=N&cursor=OPACO` con
`{"objects":[...],"next_cursor":""}`. Ambos campos son obligatorios; sólo
`objects:[]` y `next_cursor:""` explícitos acreditan cero observaciones.
`users` exige `email` y admite `name`; `devices` exige `user_id` y admite
`last_signal_at` RFC3339. Los atributos fuera de allowlist, el JSON inválido,
los redirects, las cookies, el content type distinto de JSON, la página mayor
al límite y el cuerpo mayor a 1 MiB se rechazan. Límites por conexión: página
1–500, 1.000 páginas y 100.000 registros; un error de transporte después de
una página deja una corrida parcial, mientras que un error de integridad o de
la primera página no publica observaciones.

## Identidad de Ozal y primer administrador

El receptor usa github.com/go-jose/go-jose/v4 v4.1.9 (Apache 2.0).
Leer [contrato y límites](../docs/ACCESS.md) antes de configurar. Este modo
no requiere lab.token ni acepta sus credenciales.

Copiar internal/gateway/example.json fuera del código y configurar issuer,
audience, public_origin y public_key_files con la clave pública PKIX exportada
por `ozal assertion public-key`. No copiar la clave privada de Ozal. Los paths
relativos se resuelven respecto del JSON; no poner secretos en ese archivo.
El emisor debe coincidir con el origen auth.login_url de Ozal y la audiencia
con la de su ruta. TLS y login son responsabilidad del gateway.

```sh
/tmp/custodia init --dir /ruta/nueva/custodia-data
/tmp/custodia bootstrap-admin --dir /ruta/nueva/custodia-data --gateway-config /ruta/gateway.json --subject SUJETO_OPACO_CONFIRMADO
/tmp/custodia serve --dir /ruta/nueva/custodia-data --gateway-config /ruta/gateway.json --web-dir /ruta/al/build/web
```

init crea una base vacía, sin cuentas ni fuentes. El primer administrador puede
arrancar sin concesiones; todavía falta configurar/ingerir fuentes reales.
Si hay conexiones existentes, añadir --connections con IDs explícitos separados
por coma al bootstrap: el rol no concede lectura global. El bootstrap sólo se
permite una vez, con auditoría. No hay recuperación ni interfaz administrativa
completas todavía. Para una base previa, detener el proceso y ejecutar migrate
antes de servir; no aplicar cambios silenciosos al arranque.

Un administrador autenticado puede crear un lector mediante POST
/api/access/readers con JSON {"subject":"SUJETO_OPACO","connections":["CONEXION"]}
o deshabilitarlo mediante DELETE con {"subject":"SUJETO_OPACO"}. Se requieren
aserción nueva y Origin configurado, además de las concesiones del administrador.
No usar email como sujeto ni guardar aserciones en scripts o logs.

```sh
npm run verify:go:gateway
```

Este gate crea claves, un gateway fixture, dos sujetos y una base descartables.
Comprueba entrada firmada y logout con la UI real sin interceptar APIs.
Usa init-lab exclusivamente para aportar inventario público al test; la
instalación con init nunca carga ese seed. No valida una instancia real de Ozal.
