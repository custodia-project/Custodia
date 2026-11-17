---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-17
---

# Custodia web: el frontend de DS Hub, abierto

Custodia es DS Hub renombrado y publicado como código abierto (Apache 2.0).
Desde el 08/11 la web se construye sobre el frontend de DS Hub; la interfaz
propia del 22/10 se retiró y queda en Git (`617f460`).

## Qué hay en el repo

| Carpeta | Qué es | Cómo se publica |
|---|---|---|
| `web/` | La app: shell, inicio con la rueda de fuentes, Personas, CyberArk, Netskope, Ayuda. React + Vite + TypeScript | Proyecto de Pages `custodia-demo` |
| `site/` | Sitio de producto en inglés (`/`) y español (`/es/`), generado desde `site/site/` | Proyecto de Pages `custodia` |
| `demo/` | Experimento reducido anterior | No se publica |

## Verificación en Linux y Windows

El workflow [CI](../.github/workflows/ci.yml) configura la web con Node.js 24
en Ubuntu y Windows: tipos, lint, estilos, unitarias, builds de producción y
demo, y e2e con Chromium. Los comandos de build usan Node y funcionan sin
asignaciones de variables específicas de Bash. Se ejecuta con cada push,
pull request o manualmente desde Actions una vez incorporado a GitHub.

El backend se verifica sólo en Linux: Go toma la versión de `server/go.mod`,
ejecuta vet, pruebas con detector de carreras, build y govulncheck; además se
verifican ingreso local, paquete/backup/restore, instalador y actualización con
rollback en entornos descartables. Esta matriz no declara soporte de
instalación del servidor en Windows ni publica releases.

## Dos modos de la app

- **Real:** `npm run build`. La app habla con `/api` a través de
  `web/src/cross/lib/api.ts`; en desarrollo, Vite reenvía `/api` a
  `127.0.0.1:8000`.
- **Demo:** `npm run build:demo` (define `VITE_DEMO=1`). Antes del primer
  render, `web/src/demo/install.ts` intercepta `fetch` y responde `/api`
  con `web/src/demo/mock-api.ts`. Es la misma API simulada que usan las
  pruebas e2e, así que la demo y las pruebas no pueden divergir. Nada sale del
  navegador; el build real no incluye ese código.

La demo cuenta la historia de Acme Corp: `web/src/demo/acme-api.ts` responde
desde `web/src/demo/model/company.json` las rutas de Personas, Safes, cuentas,
métricas de CyberArk e inventario de Netskope, y delega el resto en la API
simulada. Personas, equipos, Safes y contadores salen del mismo escenario que
cita el sitio (Alice: brecha; Bob: parcial; Eve: desconocido). Las fechas se
corren para que la captura parezca siempre reciente. Las pruebas e2e siguen
usando sólo `mock-api.ts`.

La demo entra con una sesión de lectura («Demo»). Una ruta que la API
simulada no conoce responde «No disponible en la demo».

## Laboratorio Go contra el build real

`npm run verify:go:web` compila el frontend con VITE_DEMO=0 en una carpeta
temporal y un binario Go sin CGO. El servidor Go sirve la UI con --web-dir y
responde las lecturas de Personas/cobertura desde SQLite. El navegador prueba
los casos Acme sin interceptar respuestas y comprueba que hubo HTTP real.
Las preferencias se conservan al reiniciar y se auditan en su transacción.

Personas comparte runtime con CyberArk pero no carga el dashboard PAM. El
laboratorio anuncia sólo las capacidades de lectura de Identity y Netskope.
El harness inyecta una credencial sintética; no implementa ingreso interactivo,
OIDC/Ozal. El gate ya prueba autorización de lectura por conexión con
principales sintéticos, acceso de una sola fuente y revocación. Consultar
[ejecución y límites](../server/README.md) y [paridad](PARITY.md).

## Idiomas

La app está en español (fuente) e inglés con `web/src/cross/i18n/`: `t('texto
en español')` busca la traducción en `en.ts` y, si falta, muestra el español.
El idioma sale de `?lang=`, luego de lo guardado; la demo publicada sigue al
navegador y la app con backend arranca en español. Cambiar de idioma recarga
la página. El sitio enlaza la demo con `?lang=` de su idioma.
`node scripts/i18n-report.mjs --missing` (en `web/`) lista lo pendiente.

## Datos

Todo nombre de la demo y las pruebas es ficticio: `acme.example`, Globex,
Initech. No se copian dominios, empresas, grupos, hosts ni remotos del
empleador de origen; ver `AGENTS.md`.

## Marca

La marca son dos registros que se cruzan (`web/public/mark.svg`, lima sobre
grafito); el favicon y el logo de la cabecera son el mismo dibujo. La app
conserva la estructura de DS Hub (rueda, círculos, accesos), pero usa la paleta
y las tipografías del sitio (`site/src/design/tokens.css`): azul tinta, lima
sólo para marca, acción, foco y selección, y los colores de veredicto para los
estados. Los tokens viven en `web/src/styles/foundation.css`; el texto sobre
lima usa `--accent-fill` y `--on-accent` en ambos temas.

## Diseño del sitio

**El sitio es la vidriera de la demo.** No muestra nada que la demo no
muestre: usa sus colores, sus logos, sus nombres de vistas y sus cuatro
etiquetas de vínculo, y sus pantallas son capturas reales de `web/`. Si un
texto o un elemento del sitio no se puede encontrar al abrir la demo, sobra.
Cuando la demo cambia, se regeneran las capturas y se revisan los textos.
Las páginas de contenido (`/inventory/`, `/connectors/`, `/install/`,
`/limits/` y sus pares) no son pantallas de la demo: explican contratos,
requisitos, versiones y límites, con el mismo sistema visual y enlaces a la
evidencia pública.

El sitio se diseña con un método, no con adjetivos. «Moderno» o «limpio» no
describen nada; cada pedido de diseño nombra:

1. **El elemento**: maqueta, tipografía, movimiento, respuesta al toque.
2. **Una referencia** que sea excelente en ese elemento, y el principio que se toma.
3. **La adaptación** a los tokens, las fuentes y la CSP de Custodia.
4. **Lo que se deja afuera.** Una referencia sin esta parte no está pensada.

Una referencia aporta principios, nunca material: no se copian logos, textos,
ilustraciones ni maquetas de otro producto.

**Dos colores tranquilos y uno fuerte.** El azul tinta es el fondo (cerca del
60 % de una pantalla) y sus tonos más claros llevan paneles, filos y texto
secundario (cerca del 30 %). El fondo es un color que se nota, no un gris casi
negro: con un neutro sin color, el sitio se lee como un modo oscuro cualquiera. El lima es el único color fuerte (cerca del 10 %)
y va sólo en el detalle que se tiene que ver primero: la marca, las palabras
clave del titular, la acción siguiente, la selección y el foco. Todo lo demás
queda tranquilo para que ese detalle resalte: etiquetas de sección, viñetas,
subrayados, filos y resplandores son decoración y nunca van en lima. Si dos
cosas compiten por la primera mirada, una sobra. Los colores de estado (verde,
ámbar, rojo) aparecen sólo donde la demo los usa.

**Profundidad con luz, no con más color.** El sitio usa el azul tinta de la
demo tal cual; la profundidad sale de una luz fría y suave desde arriba de la
página, un filo fino bajo la cabecera y la captura del inicio que se funde
con el fondo. Nada de resplandores en lima, degradados entre colores ni
ilustraciones detrás del contenido. Si el fondo cambia de color, cambia en
`site/src/design/tokens.css` y en `web/src/styles/foundation.css` a la vez.

| Elemento | Referencia | Se toma | Se deja |
|---|---|---|---|
| Maqueta de la página | stripe.com | Una afirmación por sección, con la pantalla real del producto al lado. | Degradados vistosos y muchas llamadas a la acción. |
| Tipografía | pentagram.com | Pocos tamaños, títulos firmes, medida cómoda de lectura. | Familias de exhibición; el sitio usa Instrument Sans y JetBrains Mono. |
| Minimalismo | muji.com | Quitar hasta que quede lo útil; nada que la demo no tenga. | Calidez decorativa. |
| Recorrido | linear.app | El producto real en pantalla, una idea por paso. | Brillos y degradados decorativos. |
| Animación | apple.com | El movimiento acompaña la lectura: la pantalla cambia con el paso que se lee. | Secuestrar el scroll, video automático, bucles. |
| Primer contacto | duolingo.com | Un logro real y temprano: la demo abre sin registro. | Rachas, mascotas y recordatorios con culpa. |

Reglas que siguen de ahí:

- **El movimiento tiene presupuesto.** Sólo `transform` y `opacity`, sin
  bucles, y todo quieto con `prefers-reduced-motion`. La página se lee completa
  sin JavaScript: cada paso del recorrido lleva su pantalla.
- **La lectura no depende de la animación.** Los pasos inactivos conservan
  el contraste del texto. Con movimiento reducido se muestran todas las capturas
  en su paso, y los contenidos siguen visibles sin esperar un revelado.
- **El scroll es del visitante.** El recorrido deja la pantalla fija y la
  cambia según el paso que se está leyendo, pero la página se desplaza normal.
- **Capturas, no dibujos.** El sitio no recrea la interfaz con diagramas
  propios. `site/scripts/capture-shots.mjs` saca las pantallas de la demo en
  inglés y español; el recorrido las usa a 1000x740 para que su texto se lea.
- **Capturas en móvil.** La portada acerca el vínculo mediante un recorte CSS
  de la captura original; cada imagen ofrece abrir su versión completa en otra
  pestaña. El texto del paso aparece antes de su pantalla.
- **El sitio se lee, la app se opera.** El sitio sube la escala de texto
  (base de 16 px) sin tocar colores ni familias; la app conserva su densidad.

## Verificación

`npm run verify:web` desde la raíz: tipos, lint, estilos, pruebas unitarias,
build de la demo y e2e con Chromium. El sitio se construye con
`npm run build:site`.

## Descubrimiento e indexación

El sitio publica su capa para buscadores y asistentes con archivos de
`site/public/` y el sitemap generado en el build:

- [robots.txt](../site/public/robots.txt) permite la búsqueda normal y los
  agentes de búsqueda o de lectura pedidos por una persona (`OAI-SearchBot`,
  `ChatGPT-User`, `Claude-SearchBot`, `Claude-User`, `PerplexityBot`,
  `Perplexity-User`). Separa los recolectores de entrenamiento (`GPTBot`,
  `ClaudeBot`, `Google-Extended`, `Applebot-Extended`, `CCBot`), que no hacen
  falta para aparecer en una respuesta.
- `sitemap.xml` se genera después de Vite con
  [build-sitemap.mjs](../site/scripts/build-sitemap.mjs) desde
  [content.mjs](../site/site/content.mjs): todas las páginas canónicas y sus
  pares `hreflang`; `robots.txt` lo referencia.
- [404.html](../site/public/404.html) es la página que Cloudflare Pages usa
  para una ruta inexistente. Sin ese archivo, Pages responde la portada con
  `200 text/html`, que es un soft 404 y no debe volver a pasar.
- `site/site/template.html` y `site/site/article.html` emiten JSON-LD por
  página (`WebSite`, `SoftwareApplication` y, en las páginas de contenido,
  `TechArticle` y `BreadcrumbList`). Declara el producto y sus capacidades
  actuales; no declara `softwareVersion` ni `offers` porque la primera release
  pública todavía no existe.
- `site/public/<clave>.txt` es la clave pública de IndexNow. Después de
  publicar, `npm run indexnow` lee el sitemap en vivo y envía sus URLs a
  `api.indexnow.org`; un `200` o `202` confirma que fueron aceptadas, no que
  estén indexadas. `npm run indexnow -- --dry-run` valida clave y sitemap sin
  enviar nada. Google no usa IndexNow: su camino es el sitemap y Search
  Console.
- Las etiquetas de verificación de Search Console y Bing se emiten desde
  `site/site/verification.mjs` o desde `GOOGLE_SITE_VERIFICATION` y
  `BING_SITE_VERIFICATION`. Son tokens públicos que aparecen en el HTML;
  nunca deben contener una clave de API ni un secreto.
- Cloudflare Web Analytics se activa por proyecto de Pages. El CSP de
  `site/` y `web/` permite el beacon de `static.cloudflareinsights.com` y el
  envío a `cloudflareinsights.com`; sin eso el navegador bloquea la medición.
  Web Analytics mide visitas y rutas, no eventos ni UTM: para clics de demo,
  documentación o instalación hace falta otra herramienta o una convención de
  rutas.

Las páginas de contenido son `/inventory/`, `/connectors/`,
`/connectors/cyberark-pam/`, `/connectors/wazuh/`, `/install/` y `/limits/`,
con sus pares `/es/`; los textos viven en
[articles-en.mjs](../site/site/articles-en.mjs) y
[articles-es.mjs](../site/site/articles-es.mjs). Cada página enlaza los
contratos públicos que la respaldan y separa lo implementado, lo probado, lo
aceptado y lo planificado.

El gate [verify:site](../package.json) construye el sitio y ejecuta
[check-site.mjs](../site/scripts/check-site.mjs). Falla si `robots.txt` o
`sitemap.xml` faltan o vuelven a ser HTML, si `404.html` falta o no es la
página propia, si el sitemap y los canonical divergen, o si el JSON-LD no
coincide con la página. El job `site` del
[CI](../.github/workflows/ci.yml) lo corre en cada push y pull request.

Después de publicar, la misma comprobación se hace contra el host real:

```bash
node site/scripts/check-site.mjs --live
npm run indexnow
```

`check-site` comprueba tipos de contenido, el 404 real y el JSON-LD de todas
las páginas; `indexnow` avisa a Bing y a los demás motores de IndexNow. La
verificación en Search Console, Bing Webmaster Tools y el envío del sitemap
son pasos del dueño: la indexación tarda y no está garantizada. No se publica
`llms.txt` todavía; no sustituye el contenido ni la indexación y se evaluará
como complemento cuando existan las páginas que responden preguntas.

## Entrada firmada de Ozal

El build real acepta mode ozal en auth/status: identidad y rol vienen del
backend, sin administración de contraseña local ni alta de usuarios legacy.
El cierre de sesión envía primero POST /api/auth/logout y luego un formulario
POST al path fijo /_/auth/logout del mismo origen para que Ozal reciba su cookie.
No consume URLs de redirección arbitrarias. El gate verify:go:gateway prueba
este recorrido con UI compilada, Go y gateway efímero que firma cada petición.
La instancia real de Ozal sigue pendiente de aceptación; ver [acceso](ACCESS.md).
