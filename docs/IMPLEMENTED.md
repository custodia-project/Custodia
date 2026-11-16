---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-10-22
---

# Entregas y evidencia

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
