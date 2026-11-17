<p align="center">
  <img src="site/public/mark.svg" width="72" alt="Logo de Custodia">
</p>

<h1 align="center">Custodia</h1>

<p align="center">
  <strong>El inventario de tus herramientas de seguridad, con sus métricas.</strong><br>
  Custodia conecta las herramientas que ya usás, recolecta su inventario con procedencia y muestra métricas declaradas por cada conector. Cada valor indica su fuente, su fecha y si la corrida fue completa o parcial.
</p>

<p align="center">
  <a href="https://custodia-demo.pages.dev/?lang=es">Demo en vivo</a> ·
  <a href="https://custodia.octaviosantacruz.com/es/">Sitio</a> ·
  <a href="#cómo-funciona">Cómo funciona</a> ·
  <a href="#estado-y-hoja-de-ruta">Hoja de ruta</a> ·
  <a href="docs/PRODUCT.md">Producto</a> ·
  <a href="LICENSE">Apache 2.0</a>
</p>

<p align="center"><a href="README.md">English</a> · <strong>Español</strong></p>

> **Prototipo:** la interfaz está completa y corre como demo con datos ficticios. El backend Go y los adaptadores reales están en curso.

![Custodia: una persona cruzada entre CyberArk y Netskope, con el veredicto y su evidencia](site/public/shots/es/person.png)

*La demo con una empresa ficticia: cómo se une una persona en cada herramienta y el veredicto con su evidencia.*

## El problema

Cada herramienta de seguridad tiene su consola y su forma de contar. El PAM
sabe cuántos Safes y cuentas administra; el SIEM sabe cuántos agentes reportan;
el resto queda en planillas que envejecen el mismo día. Comparar herramientas
exige abrir una por una y confiar en números sin fecha ni procedencia.

Custodia conecta esas herramientas, conserva el inventario con su origen y
publica métricas declaradas por cada conector. Un valor sin datos es «no
disponible», nunca cero; una corrida parcial se marca y no se usa para
conclusiones que necesitan el total.

## Qué responde

| Pregunta | Qué muestra Custodia |
|---|---|
| ¿Qué herramientas están conectadas y cuándo corrieron? | Inventario por conexión, colecciones, última publicación y completitud. |
| ¿Cuántos agentes, Safes o cuentas hay? | Totales y cortes por estado, con la fecha de los datos. |
| ¿Qué cambió o dejó de reportar? | Antigüedad, parcialidad y valores no disponibles, nunca un cero inventado. |
| ¿De dónde sale cada número? | Drill-down a los registros exactos que componen la métrica y su corrida. |

## Cómo funciona

1. **Conectar.** El administrador carga la herramienta desde la consola con
   URL, credenciales cifradas o una referencia, CA y colecciones. Un probe
   valida el acceso antes de agendar.
2. **Recolectar.** Un worker lee sólo las colecciones declaradas, conserva el
   ID externo, la corrida y la antigüedad, y publica de forma transaccional.
3. **Medir.** Cada conector declara sus métricas en el binario; se calculan al
   leer sobre la última publicación. Sin datos no hay cero: hay «no
   disponible».

## Principios de diseño

- **Procedencia en todo.** Cada dato nombra su fuente, ID externo, fecha y
  corrida.
- **Desconocido no es brecha.** Una captura parcial o fallida da
  «desconocido», nunca una falsa alarma. Ausencia, falta de permiso y cero
  observado son estados distintos.
- **Sin adivinanzas silenciosas.** Nombres parecidos nunca convierten dos
  registros en una identidad.
- **Las personas deciden; la sincronización no pisa.** Lo observado por las
  herramientas y lo decidido por el equipo son capas separadas.
- **Sólo lectura.** Custodia consulta tus herramientas; no las modifica.

## Para quién es

Operadores de seguridad e infraestructura que manejan varias consolas y
responden a mano las mismas preguntas de inventario: revisiones de acceso,
chequeos de cobertura, bajas y auditorías.

## Fuentes

| Implementadas | Catálogo abierto pendiente |
|---|---|
| CyberArk PAM (Safes, miembros, cuentas, plataformas, PSM) · Wazuh (agentes, grupos) | Fleet, DefectDojo, CrowdSec, OPNsense, Keycloak, MISP, OpenBao; comerciales con tenant de cliente |

Cada fuente nueva llega con su contrato, su allowlist y sus métricas. No hay
una población «Identity» en el MVP: el foco es el inventario y las métricas de
cada herramienta.

## Estado y hoja de ruta

Custodia es un MVP autohospedado en construcción. El login local, OIDC,
inventario con métricas, Wazuh aceptado contra 4.14.10, secretos cifrados e
instalador con TLS ya están implementados y probados en laboratorio. La
actualización desde la consola y el recorte del build de producción cierran la
tanda; la aceptación en un host real sigue pendiente.

| Etapa | Qué | Estado |
|---|---|---|
| Acceso | Login local, sesiones, CSRF, administración y OIDC configurable. | Hecho en laboratorio |
| Inventario | Métricas declaradas, CyberArk PAM y Wazuh con aceptación propia. | Hecho |
| Operación | Instalador, TLS, backups, actualización firmada y rollback. | Hecho en gate local |
| Distribución | Release pública, host descartable y avisos de terceros. | Pendiente |

La demo conserva los módulos heredados de DS Hub (Personas, Netskope SSE,
SIA/Risk, DSPM); el build de producción los excluye.

## Probarlo

Lo más rápido es la [demo en vivo](https://custodia-demo.pages.dev/?lang=es):
sin registro y sin que nada salga de tu navegador. Para correrla localmente:

```bash
cd web
npm ci
npm run preview:demo   # http://127.0.0.1:4184
```

El idioma se cambia desde el menú de usuario o con `?lang=es` / `?lang=en`.

## Repositorio

| Ruta | Qué |
|---|---|
| `web/` | La app: React, Vite y TypeScript. Compila contra una API real o como demo estática con API simulada ([WEB.md](docs/WEB.md)). |
| `site/` | El sitio del producto (`npm run build:site`). |
| `docs/` | Producto, arquitectura, conectores, diseño y decisiones. |
| `demo/` | Un experimento reducido anterior; no es la demo vigente. |

## Desarrollo

```bash
npm run verify:web      # tipos, lint, estilos, unitarias, build de la demo y e2e de web/
npm run build:site      # sitio del producto
npm run verify          # documentos + modelos de empresa y experimento
```

Requiere Node.js 24 o superior. Las pruebas de navegador usan Playwright con
Chromium (`npx playwright install chromium`; librerías del sistema con
`npx playwright install-deps chromium`, con sudo).

La interfaz se escribe en español y se traduce al inglés: los textos se
envuelven como `t('Texto en español')` y el inglés vive en
`web/src/cross/i18n/en.ts`. `node scripts/i18n-report.mjs --missing` (en
`web/`) lista lo que falte traducir.

## Documentación

| Tema | Documento |
|---|---|
| Alcance y etapas | [Producto](docs/PRODUCT.md) |
| Empresa ficticia, sitio y demo | [WEB.md](docs/WEB.md) · [COMPANY-DEMO.md](docs/COMPANY-DEMO.md) |
| Go y SQLite | [Arquitectura](docs/ARCHITECTURE.md) |
| Configurar y sincronizar herramientas | [Conectores](docs/CONNECTORS.md) |
| Ingreso, OIDC y QA | [Acceso](docs/ACCESS.md) |
| Interfaz y recorrido de ejemplo | [Diseño](docs/DESIGN.md) |
| Criterios para avanzar | [Validación](docs/VALIDATION.md) |
| De dónde viene lo que lleva la demo | [Procedencia](docs/PROVENANCE.md) |
| Capturas y textos ES/EN | [Presentación](docs/PRESENTATION.md) |
| Decisiones aceptadas | [Decisiones](docs/DECISIONS.md) |
| Versión anterior, qué se aprovechó y qué se retiró | [Referencia](docs/REFERENCE.md) |
| Pendientes y evidencia entregada | [Pendientes](docs/TO_IMPLEMENT.md) · [Cierres](docs/IMPLEMENTED.md) |

## Licencia y marcas

Custodia nació como herramienta interna (DS Hub) y hoy es open source con
licencia [Apache 2.0](LICENSE). CyberArk, Netskope y los demás nombres de
producto son marcas de sus dueños e identifican las herramientas que Custodia
lee; Custodia no está afiliado a ellos.

## Laboratorio del backend Go

La primera instalación autohospedada usará SQLite en disco local para evitar
un servicio de base de datos separado. Ya existe un [laboratorio Go](server/README.md)
de Personas/cobertura con datos sintéticos, inicialización explícita y API de
lectura, más adaptadores de sincronización HTTP de lectura para inventario
normalizado, Safes PAM, miembros de Safe, cuentas no secretas, plataformas,
catálogos PSM y cruce plataforma↔PSM contra fixtures locales, con worker y
agenda por conexión. No es todavía una instalación empresarial ni sustituye
la demo pública.

```bash
npm run verify:go
npm run verify:go:web  # interfaz real → API Go → SQLite temporal
npm run verify:go:ops  # backup con servicio activo, restore y paquete
npm run package:go     # binario + web + manifiesto, sin publicar
```

El modo `serve` incorpora un receptor de identidad firmada de Ozal y el alta
explícita del primer administrador. `npm run verify:go:gateway` prueba ingreso,
permisos y logout con la UI real y un gateway temporal. Falta aceptar la
integración contra una instancia real de Ozal; ver [acceso](docs/ACCESS.md).

## Instalación

El contrato de instalación, TLS, firma de releases, layout y desinstalación está en [docs/INSTALL.md](docs/INSTALL.md). El instalador de esta entrega prueba artefactos locales firmados; la publicación de releases requiere pedido explícito.
