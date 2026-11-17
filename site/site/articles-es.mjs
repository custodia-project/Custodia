// Páginas en español. Cada clave tiene su par en articles-en.mjs. Los hechos
// salen de docs/CONNECTORS.md, docs/INSTALL.md, docs/ACCESS.md y
// docs/VALIDATION.md; no convertir un conector planificado ni una prueba con
// fixture en una integración aceptada.
export const articlesEs = {
  inventory: {
    path: '/es/inventario/',
    title: 'Qué inventaría Custodia · Herramientas y métricas',
    description: 'Custodia conserva un inventario de solo lectura de herramientas, colecciones y métricas declaradas por conector, con procedencia, fecha y completitud.',
    eyebrow: 'Inventario',
    h1: 'Qué inventaría Custodia',
    headline: 'Qué inventaría Custodia',
    lede: 'Custodia conserva un inventario de solo lectura de las herramientas de seguridad que conectás, las colecciones que expone cada una y las métricas que declara su conector. Cada valor conserva su fuente, la fecha de los datos y si la corrida fue completa.',
    crumb: 'Inventario',
    status: [
      { tone: 'done', label: 'Implementado en el MVP autohospedado' },
      { tone: 'pending', label: 'Falta la primera release pública' },
    ],
    body: `
<section class="article-section" id="definition">
  <h2>Una capa de inventario, no otra consola</h2>
  <p>Custodia no es un SIEM, un PAM, un escáner de vulnerabilidades ni una herramienta de remediación. Lee de herramientas que ya corren, conserva lo que reportaron y muestra los números que declaran sus conectores. No modifica la herramienta de origen.</p>
  <p>El producto responde bien un conjunto acotado de preguntas: qué herramientas están conectadas, qué reportó cada una, qué tan fresca y completa es esa captura, y qué registros componen un número. La detección, la respuesta, la rotación de contraseñas y la gestión de tickets quedan en las herramientas que las hacen.</p>
  <dl class="article-defs">
    <div><dt>Observación</dt><dd>Un registro leído de una colección: un Safe, un agente, una fila de metadatos de cuenta. Conserva el ID externo, la conexión, la corrida y la fecha de los datos.</dd></div>
    <div><dt>Colección</dt><dd>Un conjunto acotado de registros que declara un conector, como <code>safes</code>, <code>agents</code> o <code>groups</code>.</dd></div>
    <div><dt>Publicación</dt><dd>La última corrida completa o parcial que dejó legible una colección. Una corrida fallida no reemplaza la publicación anterior.</dd></div>
    <div><dt>Métrica</dt><dd>Un cálculo declarado por el conector sobre una colección publicada, con ID versionado y una explicación legible.</dd></div>
  </dl>
</section>

<section class="article-section" id="what-it-keeps">
  <h2>Qué conserva</h2>
  <p>Cada conexión es una instancia configurada de una herramienta soportada. La conexión nombra su URL, una referencia de credencial o un secreto cifrado, la CA y las colecciones elegidas. El inventario las mantiene separadas: dos tenants del mismo producto son dos conexiones, con sus propias corridas y permisos.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Pregunta</th><th>Qué muestra el inventario</th></tr></thead>
      <tbody>
        <tr><td>¿Qué herramientas están conectadas y cuándo corrieron?</td><td>Conexión, conector, colecciones, última publicación y completitud.</td></tr>
        <tr><td>¿Cuántos agentes, Safes o cuentas hay?</td><td>Totales y cortes por estado, con la fecha de los datos.</td></tr>
        <tr><td>¿Qué cambió o dejó de reportar?</td><td>Antigüedad, parcialidad y valores no disponibles. Nunca un cero inventado.</td></tr>
        <tr><td>¿De dónde sale cada número?</td><td>Un detalle con los registros exactos que componen la métrica y la corrida detrás.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Las decisiones humanas son una capa aparte. Una clasificación, un responsable o una nota de seguimiento no pisan lo que observó la herramienta, y una sincronización posterior no borra la decisión.</p>
</section>

<section class="article-section" id="metrics">
  <h2>Cómo se calcula una métrica</h2>
  <p>Cada conector declara sus métricas en el binario, junto a sus colecciones. El administrador no las define en el MVP, y una métrica no puede leer el payload crudo del proveedor ni un campo fuera de la allowlist del conector.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Cálculo</th><th>Qué significa</th></tr></thead>
      <tbody>
        <tr><td><code>count</code></td><td>Registros activos de la colección publicada.</td></tr>
        <tr><td><code>count_where</code></td><td>Registros activos cuyo campo allowlisted es igual a uno de los valores declarados. Un registro sin el campo no cuenta.</td></tr>
        <tr><td><code>ratio</code></td><td>Un porcentaje con numerador y denominador explícitos, redondeado a un decimal.</td></tr>
        <tr><td><code>age_over</code></td><td>Registros cuyo campo RFC 3339 es anterior a un umbral fijo de días, medido desde la fecha de publicación.</td></tr>
        <tr><td><code>count_without</code></td><td>Registros de una colección que ningún registro de una colección hermana referencia por el campo declarado.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Las métricas se calculan al leer sobre la última publicación. No hay tabla de métricas ni un valor materializado que pueda separarse de los registros. Una corrida parcial conserva su valor y lo marca como parcial; una proporción o un <code>count_without</code> cuyo denominador o referencia es parcial no se calcula.</p>
  <p class="article-callout"><strong>Sin datos no es cero.</strong> Sin una colección configurada o sin publicación, la métrica es «no disponible». Cero es un valor sólo cuando una publicación confirma que ningún registro cumple el cálculo. Cada valor lleva la fecha de los datos y el estado de la corrida de cada fuente que lo compone.</p>
</section>

<section class="article-section" id="example">
  <h2>Un ejemplo verificable</h2>
  <p>El conector de Wazuh se aceptó contra un manager de laboratorio con Wazuh 4.14.10 y datos sintéticos. El manager reportó cuatro agentes y tres grupos. El conector publicó ambas colecciones completas y produjo estos valores:</p>
  <div class="table-wrap">
    <table class="table is-compact is-fit">
      <thead><tr><th>Métrica</th><th>Valor</th></tr></thead>
      <tbody>
        <tr><td>Agentes totales</td><td>4</td></tr>
        <tr><td>Agentes activos</td><td>1</td></tr>
        <tr><td>Agentes pendientes</td><td>0</td></tr>
        <tr><td>Agentes desconectados</td><td>1</td></tr>
        <tr><td>Agentes que nunca se conectaron</td><td>2</td></tr>
        <tr><td>Agentes sin contacto por más de 7 días</td><td>0</td></tr>
        <tr><td>Grupos totales</td><td>3</td></tr>
        <tr><td>Grupos vacíos</td><td>2</td></tr>
      </tbody>
    </table>
  </div>
  <p>Es un ejemplo de aceptación en una instancia de laboratorio con datos sintéticos, no un tenant de cliente. Muestra el contrato: los números suman contra los registros, la fecha de los datos está visible y la corrida es completa. Una página parcial habría conservado la publicación anterior y cambiado el estado de completitud en lugar de fingir que los registros faltantes desaparecieron.</p>
</section>

<section class="article-section" id="not">
  <h2>Qué no hace</h2>
  <ul>
    <li>No remedia, no rota contraseñas, no aísla equipos ni cambia registros en la herramienta de origen.</li>
    <li>No combina métricas entre herramientas en el MVP. Un número pertenece a un conector y a una conexión.</li>
    <li>No reemplaza la consola de origen ni su historial. Conserva la proyección acotada que se le permitió leer.</li>
    <li>No infiere una identidad por nombres parecidos. Los cruces entre herramientas necesitan una regla explícita y evidencia.</li>
    <li>No lee alertas ni el indexer de Wazuh, ni contraseñas de cuentas de CyberArk. Están fuera de los contratos actuales.</li>
  </ul>
  <p>Hay dos conectores implementados: CyberArk PAM y Wazuh. La <a href="/es/conectores/">página de conectores</a> separa los contratos implementados de las entradas planificadas del catálogo y explica por qué una herramienta planificada no se puede configurar.</p>
</section>
`,
    docs: [
      { label: 'Contratos de conectores y reglas de métricas', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md' },
      { label: 'Alcance y principios del producto', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/PRODUCT.md' },
    ],
    related: [
      { label: 'Conectores: qué puede leer Custodia', href: '/es/conectores/' },
      { label: 'CyberArk PAM: qué lee Custodia', href: '/es/conectores/cyberark-pam/' },
      { label: 'Wazuh: agentes y grupos', href: '/es/conectores/wazuh/' },
    ],
  },

  connectors: {
    path: '/es/conectores/',
    title: 'Conectores · CyberArk PAM, Wazuh y herramientas planificadas',
    description: 'Conectores de Custodia: contratos versionados de solo lectura. CyberArk PAM y Wazuh están implementados; las herramientas planificadas no tienen soporte.',
    eyebrow: 'Conectores',
    h1: 'Conectores: qué puede leer Custodia',
    headline: 'Conectores: qué puede leer Custodia',
    lede: 'Un conector es un contrato versionado de solo lectura para una herramienta. Una conexión es una instancia configurada de esa herramienta. El catálogo lista contratos implementados y herramientas planificadas, y la diferencia importa.',
    crumb: 'Conectores',
    status: [
      { tone: 'done', label: 'Dos conectores implementados' },
      { tone: 'pending', label: 'Falta un tenant real de CyberArk' },
      { tone: 'planned', label: 'Otros conectores planificados' },
    ],
    body: `
<section class="article-section" id="what-is-a-connector">
  <h2>Tres identidades distintas</h2>
  <dl class="article-defs">
    <div><dt>Proveedor</dt><dd>Quién ofrece la herramienta: CyberArk, Wazuh, un fabricante de firewall.</dd></div>
    <div><dt>Tipo de conector</dt><dd>El contrato que sabe leer una API. Declara autenticación, endpoints, colecciones, allowlists y métricas.</dd></div>
    <div><dt>Conexión</dt><dd>Una instancia configurada: un tenant, URL, credencial, CA, alcance y agenda. Dos tenants del mismo producto son dos conexiones.</dd></div>
  </dl>
  <p>Esa separación es la razón por la que «CyberArk» no es una conexión y por la que agregar un segundo tenant no copia secretos, checkpoints ni permisos del primero. Una conexión pertenece a la instalación del operador; Custodia no es un servicio multiinquilino central.</p>
</section>

<section class="article-section" id="how-a-connection-works">
  <h2>Cómo funciona una conexión</h2>
  <ol>
    <li>Elegir un conector implementado y un nombre local para la conexión.</li>
    <li>Indicar la URL base HTTPS y una referencia de credencial o un secreto write-only. Agregar una CA si la herramienta usa una privada.</li>
    <li>Ejecutar un probe. Reporta fallas de conectividad, autenticación y permisos por endpoint; un 200 en un endpoint no acredita todo el conector.</li>
    <li>Elegir las colecciones a leer. El plan del probe cubre la unión de sus endpoints.</li>
    <li>Ejecutar la primera sincronización. El worker lee sólo las colecciones declaradas y publica de forma transaccional.</li>
    <li>Revisar la última corrida completa o parcial y habilitar una agenda. Cualquier cambio de URL, autenticación, CA o secreto apaga la agenda hasta que pase un probe nuevo.</li>
  </ol>
  <p>Los secretos son write-only desde la consola. Un secreto bearer pegado se cifra con XChaCha20-Poly1305 antes de llegar a SQLite; la API, la auditoría y el worker nunca devuelven el valor. Las referencias externas <code>env:</code>, <code>file:</code> y <code>secret:</code> siguen disponibles. La clave maestra vive fuera de la base y fuera del backup por defecto.</p>
  <p class="article-callout"><strong>Solo lectura es un contrato, no una opción.</strong> El adaptador usa GET y consultas POST documentadas de lectura. No llama a endpoints de alta, edición o borrado, y no carga plugins ni scripts de terceros.</p>
</section>

<section class="article-section" id="implemented">
  <h2>Implementado hoy</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Conector</th><th>Colecciones</th><th>Aceptado contra</th><th>Falta</th></tr></thead>
      <tbody>
        <tr>
          <td><a href="/es/conectores/cyberark-pam/">CyberArk PAM</a><br /><code>cyberark-pam-safes-v1</code></td>
          <td>Safes, miembros de Safe, metadatos no secretos de cuentas, plataformas, servidores y conectores PSM, y cruces plataforma↔PSM.</td>
          <td>Fixtures locales que siguen la forma documentada de la API de CyberArk.</td>
          <td>Un tenant real de cliente con credenciales de solo lectura autorizadas.</td>
        </tr>
        <tr>
          <td><a href="/es/conectores/wazuh/">Wazuh</a><br /><code>wazuh</code></td>
          <td>Agentes y grupos.</td>
          <td>Wazuh 4.14.10, manager y agente, en una instancia de laboratorio.</td>
          <td>Soporte de cluster y versiones fuera del contrato revisado.</td>
        </tr>
      </tbody>
    </table>
  </div>
  <p>La fila de CyberArk es deliberadamente precisa: el contrato y sus fixtures existen, pero Custodia no leyó un tenant real de cliente. La fila de Wazuh tiene una aceptación real contra la versión nombrada. Ninguna fila acredita todas las funciones del producto del fabricante.</p>
</section>

<section class="article-section" id="planned">
  <h2>Planificado no es soportado</h2>
  <p>El catálogo también lista herramientas de código abierto y comerciales que no están implementadas. Un descriptor planificado no declara endpoints, colecciones ni credenciales, y la API de alta lo rechaza. Listar una herramienta es una dirección, no un contrato.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Candidatas de código abierto</th><th>Comerciales, con tenant de cliente</th></tr></thead>
      <tbody>
        <tr>
          <td>Fleet, DefectDojo, CrowdSec, OPNsense, Keycloak, MISP, OpenBao.</td>
          <td>Netskope, IBM QRadar, CrowdStrike Falcon, FortiMail, FortiGate, Palo Alto Networks, Tenable.</td>
        </tr>
      </tbody>
    </table>
  </div>
  <p>Cada conector nuevo necesita su contrato, fixtures sintéticos, allowlist, métricas declaradas y aceptación contra una instancia que el proyecto pueda leer. El proyecto no copia código ni fixtures de proveedores sin revisar la licencia, y no inventa una API para una herramienta que no probó.</p>
</section>

<section class="article-section" id="limits">
  <h2>Qué no hace un conector</h2>
  <ul>
    <li>No escribe en la herramienta de origen ni ejecuta remediaciones.</li>
    <li>No normaliza todos los campos de todas las APIs en un modelo universal. Cada contrato tiene una allowlist acotada.</li>
    <li>No fusiona registros de herramientas distintas por nombres parecidos.</li>
    <li>No expone secretos, payloads crudos ni campos fuera de la allowlist en el inventario o el detalle.</li>
    <li>No promete un conector que sólo aparece en el catálogo.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Contrato de conectores, catálogo y métricas', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md' },
      { label: 'Secretos y gestión de conexiones', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#secretos-de-conexión-y-gestión' },
    ],
    related: [
      { label: 'Qué inventaría Custodia', href: '/es/inventario/' },
      { label: 'CyberArk PAM', href: '/es/conectores/cyberark-pam/' },
      { label: 'Wazuh', href: '/es/conectores/wazuh/' },
    ],
  },

  'cyberark-pam': {
    path: '/es/conectores/cyberark-pam/',
    title: 'Conector CyberArk PAM · Safes, miembros, cuentas y PSM',
    description: 'Custodia lee metadatos acotados de CyberArk PAM: Safes, miembros, metadatos no secretos de cuentas, plataformas y catálogos PSM. Nunca lee contraseñas ni secretos.',
    eyebrow: 'CyberArk PAM',
    h1: 'CyberArk PAM: qué lee Custodia',
    headline: 'CyberArk PAM: qué lee Custodia',
    lede: 'Custodia lee un conjunto acotado de metadatos de CyberArk PAM: Safes, miembros de Safe, metadatos no secretos de cuentas, plataformas y catálogos PSM. Nunca lee ni guarda contraseñas de cuentas.',
    crumb: 'CyberArk PAM',
    status: [
      { tone: 'done', label: 'Contrato y fixtures probados' },
      { tone: 'pending', label: 'Falta un tenant real de cliente' },
      { tone: 'done', label: 'Solo lectura' },
    ],
    body: `
<section class="article-section" id="what-it-reads">
  <h2>Qué lee</h2>
  <p>El conector es <code>cyberark-pam-safes-v1</code>. Sigue la forma documentada de la API REST de CyberArk PAM y conserva sólo los campos de su allowlist. Un campo que no está en la allowlist se descarta antes de llegar al inventario.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Colección</th><th>Endpoint</th><th>Qué conserva</th></tr></thead>
      <tbody>
        <tr><td><code>safes</code></td><td><code>GET /API/Safes</code></td><td>Número y nombre del Safe, ubicación, creador, retención, auto-purge, fechas de alta y modificación, CPM administrador y bandera de vencimiento.</td></tr>
        <tr><td><code>safe_members</code></td><td><code>GET /API/Safes/{safeName}/Members</code></td><td>Identidad del miembro, tipo, vencimiento, banderas de predefinido/sólo lectura y el objeto acotado de permisos.</td></tr>
        <tr><td><code>accounts</code></td><td><code>GET /API/Accounts</code></td><td>Metadatos no secretos: ID y nombre de cuenta, usuario, dirección, Safe, plataforma, tipo, categoría y estado de gestión automática.</td></tr>
        <tr><td><code>platforms</code></td><td><code>GET /API/Platforms</code></td><td>ID y nombre de plataforma, bandera activa, plataforma base, tipo, tipo de sistema y referencia al servidor PSM.</td></tr>
        <tr><td><code>psm_servers</code></td><td><code>GET /API/PSM/Servers</code></td><td>ID, nombre y dirección del servidor PSM.</td></tr>
        <tr><td><code>psm_connectors</code></td><td><code>GET /API/PSM/Connectors</code></td><td>ID y nombre visible del conector PSM.</td></tr>
        <tr><td><code>platform_psm_links</code></td><td>Calculado desde plataformas y servidores PSM</td><td>Referencia exacta, con cambio de mayúsculas, ambigua o faltante entre una plataforma y un servidor PSM.</td></tr>
      </tbody>
    </table>
  </div>
  <p class="article-callout"><strong>Nunca lee:</strong> contraseñas de cuentas, valores de secretos, tickets, sesiones, <code>platformAccountProperties</code>, motivos de gestión manual, campos de último cambio por un usuario ni el payload crudo del proveedor. Las cuentas sin un Safe visible o sin ID usable se omiten; la corrida queda parcial y no retira cuentas.</p>
</section>

<section class="article-section" id="requirements">
  <h2>Requisitos</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th>URL base</th><td>HTTPS con un prefijo de path opcional como <code>/PasswordVault</code>. Sin usuario, query ni fragmento embebidos.</td></tr>
        <tr><th>Identidad de solo lectura</th><td>Una cuenta autorizada a leer las colecciones elegidas. Custodia no necesita permisos de escritura, rotación ni sesiones.</td></tr>
        <tr><th>Autenticación</th><td>Bearer, OAuth client-credentials o mTLS. El probe valida el modo elegido; un secreto bearer se puede guardar write-only y cifrado.</td></tr>
        <tr><th>Referencias de credencial</th><td>Referencias <code>env:</code>, <code>file:</code> o <code>secret:</code>; las credenciales literales se rechazan. <code>file:</code> y <code>secret:</code> se resuelven sólo dentro del directorio de secretos configurado.</td></tr>
        <tr><th>TLS</th><td>La CA del tenant se puede entregar como archivo. No se siguen redirects ni enlaces de paginación fuera del mismo origen con credenciales.</td></tr>
      </tbody>
    </table>
  </div>
  <p>La conexión se crea con una URL HTTPS y una referencia de credencial; el probe y el worker usan el mismo plan acotado. Un cambio de URL, autenticación, CA o secreto invalida el probe y apaga la agenda.</p>
</section>

<section class="article-section" id="metrics">
  <h2>Métricas declaradas</h2>
  <p>El conector declara estas métricas sobre sus colecciones publicadas. Se calculan al leer; ninguna lee un secreto ni el payload crudo.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Métrica</th><th>ID</th><th>Cálculo</th></tr></thead>
      <tbody>
        <tr><td>Safes totales</td><td><code>cyberark-pam.safes.total.v1</code></td><td><code>count</code> sobre <code>safes</code>.</td></tr>
        <tr><td>Safes sin miembros</td><td><code>cyberark-pam.safes.without_members.v1</code></td><td><code>count_without</code> sobre <code>safes</code> y <code>safe_members</code>. No se calcula si la colección de miembros es parcial.</td></tr>
        <tr><td>Cuentas totales</td><td><code>cyberark-pam.accounts.total.v1</code></td><td><code>count</code> sobre <code>accounts</code>.</td></tr>
        <tr><td>Cuentas con gestión automática deshabilitada</td><td><code>cyberark-pam.accounts.auto_management_disabled.v1</code></td><td><code>count_where automatic_management_enabled=false</code>.</td></tr>
        <tr><td>Proporción de cuentas sin gestión automática</td><td><code>cyberark-pam.accounts.auto_management_disabled_ratio.v1</code></td><td>Proporción sobre las cuentas que informan <code>true</code> o <code>false</code>; el denominador puede ser menor que el total de cuentas.</td></tr>
        <tr><td>Cuentas con último cambio fallido</td><td><code>cyberark-pam.accounts.last_change_failed.v1</code></td><td><code>count_where management_status=failure</code>.</td></tr>
        <tr><td>Plataformas inactivas</td><td><code>cyberark-pam.platforms.inactive.v1</code></td><td><code>count_where active=false</code>.</td></tr>
        <tr><td>Plataformas con PSM sin resolver</td><td><code>cyberark-pam.platforms.psm_unresolved.v1</code></td><td><code>count_where resolution</code> es <code>missing</code> o <code>ambiguous</code> sobre <code>platform_psm_links</code>.</td></tr>
      </tbody>
    </table>
  </div>
  <p>El cruce plataforma↔PSM es evidencia, no una adivinanza. Una fila <code>ambiguous</code> registra por qué no se pudo elegir entre dos IDs del catálogo; no es una arista. Una fila <code>missing</code> conserva la referencia cruda en lugar de inventar un destino.</p>
</section>

<section class="article-section" id="limits">
  <h2>Límites actuales</h2>
  <ul>
    <li><strong>Todavía no hay un tenant real aceptado.</strong> El adaptador se probó contra fixtures locales que siguen la forma documentada de la API. Falta un tenant de cliente con credenciales de solo lectura autorizadas para llamar aceptada a la integración.</li>
    <li>Una conexión representa un tenant. Las colecciones de la misma conexión comparten URL, credencial, CA y autenticación.</li>
    <li>Sólo se leen las colecciones listadas. El detalle de plataformas y <code>AllowedSafes</code> quedan fuera de este contrato.</li>
    <li>Sin escrituras, rotación de contraseñas, gestión de sesiones ni datos de SIA/Risk en el build de producción.</li>
    <li>Las métricas son por conector. No hay métricas combinadas de CyberArk y Wazuh en el MVP.</li>
    <li>Una corrida parcial nunca retira registros; una corrida fallida no reemplaza la última publicación.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Contrato CyberArk PAM y pruebas con fixture', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#segundo-adaptador-cyberark-pam-safes-fixture' },
      { label: 'Métricas declaradas de CyberArk', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#métricas-declaradas-de-cyberark-pam' },
    ],
    related: [
      { label: 'Wazuh: agentes y grupos', href: '/es/conectores/wazuh/' },
      { label: 'Qué inventaría Custodia', href: '/es/inventario/' },
      { label: 'Límites actuales', href: '/es/limites/' },
    ],
  },

  wazuh: {
    path: '/es/conectores/wazuh/',
    title: 'Conector Wazuh · Agentes y grupos, no alertas',
    description: 'Custodia lee agentes y grupos de Wazuh por la API del manager. Aceptado contra Wazuh 4.14.10; alertas, indexer y cluster quedan fuera de este contrato.',
    eyebrow: 'Wazuh',
    h1: 'Wazuh: agentes y grupos, no alertas',
    headline: 'Wazuh: agentes y grupos, no alertas',
    lede: 'Custodia lee la API del manager de Wazuh para inventariar agentes y grupos. No lee el indexer ni las alertas, y no modifica agentes.',
    crumb: 'Wazuh',
    status: [
      { tone: 'done', label: 'Aceptado contra Wazuh 4.14.10' },
      { tone: 'pending', label: 'Un solo manager' },
      { tone: 'neutral', label: 'Alertas e indexer fuera de alcance' },
    ],
    body: `
<section class="article-section" id="what-it-reads">
  <h2>Qué lee</h2>
  <p>El conector inicia sesión en la API del manager con autenticación Basic, recibe un JWT válido por 900 segundos y lo mantiene sólo en memoria durante el probe o la corrida. Nunca persiste el token ni lo renueva; una corrida que excede la vigencia falla cerrada y se reintenta.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Llamada</th><th>Uso</th><th>Qué conserva</th></tr></thead>
      <tbody>
        <tr><td><code>POST /security/user/authenticate</code></td><td>Autenticación Basic</td><td>No se guarda el token; vive sólo en memoria durante la operación.</td></tr>
        <tr><td><code>GET /manager/info</code></td><td>Sólo probe</td><td>Identidad del manager para la comprobación de acceso.</td></tr>
        <tr><td><code>GET /agents</code></td><td>Colección <code>agents</code></td><td>ID externo, nombre, estado, IPs, sistema operativo, versión del agente, grupos, cantidad de grupos, último keepalive, alta y manager/nodo.</td></tr>
        <tr><td><code>GET /groups</code></td><td>Colección <code>groups</code></td><td>Nombre del grupo y cantidad de agentes.</td></tr>
      </tbody>
    </table>
  </div>
  <p>La configuración de agentes, las claves, los datos de alertas y el payload completo del proveedor no forman parte de la allowlist. La API pagina con <code>limit</code>, <code>offset</code> y <code>sort</code>; el conector comprueba el total informado y falla si una página o el conteo son inconsistentes.</p>
</section>

<section class="article-section" id="requirements">
  <h2>Requisitos</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th>Versión de Wazuh</th><td>4.14.10 es la versión aceptada. Otra versión mayor necesita revisar el contrato antes de declararla disponible.</td></tr>
        <tr><th>Endpoint de API</th><td>La API del manager por HTTPS, normalmente en el puerto 55000, con un archivo de CA si el certificado es privado.</td></tr>
        <tr><th>Usuario de solo lectura</th><td>Un usuario de API con <code>agent:read</code>, <code>group:read</code> y <code>manager:read</code>. Crear un agente debe estar denegado.</td></tr>
        <tr><th>Alcance</th><td>Un manager. Los nodos de cluster no se combinan en este contrato.</td></tr>
        <tr><th>Escrituras</th><td>Ninguna. Custodia no crea, reinicia ni elimina agentes, y no cambia la membresía de grupos.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="metrics">
  <h2>Métricas declaradas</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Métrica</th><th>Cálculo</th></tr></thead>
      <tbody>
        <tr><td>Agentes totales</td><td><code>count</code> sobre <code>agents</code>.</td></tr>
        <tr><td>Agentes activos</td><td><code>count_where status=active</code>.</td></tr>
        <tr><td>Agentes pendientes</td><td><code>count_where status=pending</code>.</td></tr>
        <tr><td>Agentes desconectados</td><td><code>count_where status=disconnected</code>.</td></tr>
        <tr><td>Agentes que nunca se conectaron</td><td><code>count_where status=never_connected</code>.</td></tr>
        <tr><td>Agentes sin contacto por más de 7 días</td><td><code>age_over</code> sobre <code>last_keep_alive</code> con umbral fijo de 7 días. Un agente sin keepalive no cuenta como antiguo.</td></tr>
        <tr><td>Grupos totales</td><td><code>count</code> sobre <code>groups</code>.</td></tr>
        <tr><td>Grupos vacíos</td><td><code>count_where agent_count=0</code>.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="acceptance">
  <h2>Ejemplo de aceptación</h2>
  <p>El conector se aceptó el 16 de noviembre de 2026 contra un manager y un agente de laboratorio, ambos Wazuh 4.14.10, con datos sintéticos. El manager reportó cuatro agentes y tres grupos. El probe tuvo cero endpoints fallidos; las publicaciones fueron completas, con cuatro y tres registros.</p>
  <div class="table-wrap">
    <table class="table is-compact is-fit">
      <thead><tr><th>Métrica</th><th>Valor</th></tr></thead>
      <tbody>
        <tr><td>Agentes totales</td><td>4</td></tr>
        <tr><td>Agentes activos</td><td>1</td></tr>
        <tr><td>Agentes pendientes</td><td>0</td></tr>
        <tr><td>Agentes desconectados</td><td>1</td></tr>
        <tr><td>Agentes que nunca se conectaron</td><td>2</td></tr>
        <tr><td>Agentes sin contacto por más de 7 días</td><td>0</td></tr>
        <tr><td>Grupos totales</td><td>3</td></tr>
        <tr><td>Grupos vacíos</td><td>2</td></tr>
      </tbody>
    </table>
  </div>
  <p>Es una aceptación de laboratorio, no un tenant de cliente. La conexión, el secreto cifrado, la CA, el probe, la agenda y el inventario se recorrieron desde la consola con el mismo código que usa el producto.</p>
</section>

<section class="article-section" id="limits">
  <h2>Límites actuales</h2>
  <ul>
    <li>Un manager; el soporte de cluster no está implementado.</li>
    <li>Sin alertas y sin indexer. Los datos de alertas de Wazuh viven fuera de esta API y no se leen ni se inventan.</li>
    <li>Sin escrituras ni acciones sobre agentes. Custodia sólo lee.</li>
    <li>El JWT se renueva por probe o corrida; no se persiste.</li>
    <li>Las versiones fuera del contrato revisado de 4.14.10 necesitan una revisión nueva.</li>
    <li>Sin métricas combinadas de Wazuh y CyberArk en el MVP.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Contrato, endpoints y aceptación de Wazuh', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#tercer-adaptador-wazuh-agentes-y-grupos' },
      { label: 'Notas de la versión 4.14.10', href: 'https://documentation.wazuh.com/current/release-notes/release-4-14-10.html' },
      { label: 'Referencia de la API de Wazuh', href: 'https://documentation.wazuh.com/current/user-manual/api/reference.html' },
    ],
    related: [
      { label: 'CyberArk PAM: qué lee Custodia', href: '/es/conectores/cyberark-pam/' },
      { label: 'Qué inventaría Custodia', href: '/es/inventario/' },
      { label: 'Límites actuales', href: '/es/limites/' },
    ],
  },

  metrics: {
    path: '/es/metricas/',
    title: 'Referencia de métricas · CyberArk PAM y Wazuh',
    description: 'Todas las métricas que Custodia declara en el MVP: fuente, ID, cálculo, fecha de los datos y reglas de completitud para CyberArk PAM y Wazuh.',
    eyebrow: 'Métricas',
    h1: 'Las métricas que declara Custodia',
    headline: 'Las métricas que declara Custodia',
    lede: 'Una referencia de cada métrica del MVP: qué cuenta, de qué colección sale, cómo cambia la respuesta con datos parciales y dónde está la documentación de origen.',
    crumb: 'Métricas',
    status: [
      { tone: 'done', label: 'Contrato CyberArk PAM probado' },
      { tone: 'done', label: 'Wazuh 4.14.10 aceptado' },
      { tone: 'pending', label: 'Métricas personalizadas fuera del MVP' },
    ],
    body: `
<section class="article-section" id="how">
  <h2>Cómo se calcula una métrica</h2>
  <p>Cada conector declara sus métricas en el binario, junto a sus colecciones. Una métrica tiene ID versionado, explicación legible, colección de origen, cálculo y unidad. El administrador no define métricas en el MVP, y una métrica no puede leer el payload crudo del proveedor ni un campo fuera de la allowlist del conector.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Cálculo</th><th>Qué significa</th></tr></thead>
      <tbody>
        <tr><td><code>count</code></td><td>Registros activos de la colección publicada.</td></tr>
        <tr><td><code>count_where</code></td><td>Registros activos cuyo campo allowlisted es igual a uno de los valores declarados. Un registro sin el campo no cuenta.</td></tr>
        <tr><td><code>ratio</code></td><td>Porcentaje con numerador y denominador explícitos, redondeado a un decimal.</td></tr>
        <tr><td><code>age_over</code></td><td>Registros cuyo campo RFC 3339 es anterior a un umbral fijo de días, medido desde la fecha de publicación.</td></tr>
        <tr><td><code>count_without</code></td><td>Registros de una colección que ningún registro de una colección hermana referencia por el campo declarado.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Las métricas se calculan al leer sobre la última publicación. No hay tabla de métricas ni valor materializado que pueda separarse de los registros. Cambiar un cálculo crea una versión nueva; un valor histórico no se reinterpreta.</p>
</section>

<section class="article-section" id="cyberark">
  <h2>CyberArk PAM</h2>
  <p>Contrato <code>cyberark-pam-safes-v1</code>. El conector se probó contra fixtures locales que siguen la forma documentada de la API; todavía falta un tenant real de cliente.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Métrica e ID</th><th>Cálculo</th><th>Notas</th></tr></thead>
      <tbody>
        <tr><td>Safes totales<br /><code>cyberark-pam.safes.total.v1</code></td><td><code>count</code></td><td>Safes activos en <code>safes</code>.</td></tr>
        <tr><td>Safes sin miembros<br /><code>cyberark-pam.safes.without_members.v1</code></td><td><code>count_without</code></td><td>Compara <code>safes</code> con <code>safe_members</code>; no se calcula si la colección de miembros es parcial.</td></tr>
        <tr><td>Cuentas totales<br /><code>cyberark-pam.accounts.total.v1</code></td><td><code>count</code></td><td>Metadatos no secretos de cuentas en <code>accounts</code>.</td></tr>
        <tr><td>Cuentas con gestión automática deshabilitada<br /><code>cyberark-pam.accounts.auto_management_disabled.v1</code></td><td><code>count_where automatic_management_enabled=false</code></td><td>Una bandera ausente no cuenta.</td></tr>
        <tr><td>Proporción sin gestión automática<br /><code>cyberark-pam.accounts.auto_management_disabled_ratio.v1</code></td><td><code>ratio</code></td><td>Denominador: cuentas que informan <code>true</code> o <code>false</code>. Puede ser menor que el total de cuentas.</td></tr>
        <tr><td>Cuentas con último cambio fallido<br /><code>cyberark-pam.accounts.last_change_failed.v1</code></td><td><code>count_where management_status=failure</code></td><td>El estado se normaliza a success, failure o in process.</td></tr>
        <tr><td>Plataformas inactivas<br /><code>cyberark-pam.platforms.inactive.v1</code></td><td><code>count_where active=false</code></td><td>Las activas son el complemento entre las plataformas que informan la bandera.</td></tr>
        <tr><td>Plataformas con PSM sin resolver<br /><code>cyberark-pam.platforms.psm_unresolved.v1</code></td><td><code>count_where resolution</code></td><td>Cuenta <code>missing</code> y <code>ambiguous</code> en <code>platform_psm_links</code>; no inventa un destino.</td></tr>
      </tbody>
    </table>
  </div>
  <p>El cruce plataforma↔PSM distingue <code>exact</code>, <code>casefold</code>, <code>ambiguous</code> y <code>missing</code>. Una fila ambigua registra por qué no se pudieron elegir dos IDs; no es una arista. Ver <a href="/es/conectores/cyberark-pam/">la página de CyberArk</a> para endpoints, allowlist y límites.</p>
</section>

<section class="article-section" id="wazuh">
  <h2>Wazuh</h2>
  <p>Aceptado contra Wazuh 4.14.10 con un manager y un agente de laboratorio. Un manager; alertas e indexer quedan fuera de este conector.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Métrica e ID</th><th>Cálculo</th><th>Notas</th></tr></thead>
      <tbody>
        <tr><td>Agentes totales<br /><code>wazuh.agents.total.v1</code></td><td><code>count</code></td><td>Registros activos en <code>agents</code>.</td></tr>
        <tr><td>Agentes activos<br /><code>wazuh.agents.active.v1</code></td><td><code>count_where status=active</code></td><td>Estado actual del agente.</td></tr>
        <tr><td>Agentes pendientes<br /><code>wazuh.agents.pending.v1</code></td><td><code>count_where status=pending</code></td><td>Estado actual del agente.</td></tr>
        <tr><td>Agentes desconectados<br /><code>wazuh.agents.disconnected.v1</code></td><td><code>count_where status=disconnected</code></td><td>Estado actual del agente.</td></tr>
        <tr><td>Agentes que nunca se conectaron<br /><code>wazuh.agents.never_connected.v1</code></td><td><code>count_where status=never_connected</code></td><td>Estado actual del agente.</td></tr>
        <tr><td>Agentes sin contacto por más de 7 días<br /><code>wazuh.agents.stale_7d.v1</code></td><td><code>age_over</code></td><td>Umbral fijo de 7 días sobre <code>last_keep_alive</code>. Un agente sin keepalive no cuenta como antiguo.</td></tr>
        <tr><td>Grupos totales<br /><code>wazuh.groups.total.v1</code></td><td><code>count</code></td><td>Grupos en <code>groups</code>.</td></tr>
        <tr><td>Grupos vacíos<br /><code>wazuh.groups.empty.v1</code></td><td><code>count_where agent_count=0</code></td><td>Un grupo con cantidad confirmada en cero.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Los estados salen del manager. Un keepalive ausente no es evidencia de antigüedad: se informa como desconocido, no como agente viejo. Ver <a href="/es/conectores/wazuh/">la página de Wazuh</a> para API, RBAC y el ejemplo de aceptación.</p>
</section>

<section class="article-section" id="states">
  <h2>Disponible, parcial y no disponible</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Estado</th><th>Qué significa</th></tr></thead>
      <tbody>
        <tr><td>Disponible</td><td>La colección tiene una publicación completa y el cálculo tiene todas sus entradas.</td></tr>
        <tr><td>Parcial</td><td>El valor se muestra con la marca de parcial. Una proporción o un <code>count_without</code> cuyo denominador o referencia es parcial no se calcula.</td></tr>
        <tr><td>No disponible</td><td>No hay colección configurada o no hay publicación. Nunca se muestra como cero.</td></tr>
        <tr><td>Cero confirmado</td><td>La publicación contiene explícitamente ningún registro que cumpla el cálculo. Cero es un valor real sólo entonces.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Cada valor lleva la fecha de los datos y el estado de la corrida de sus fuentes. El detalle abre los registros exactos que componen la métrica, con los mismos permisos de la conexión. Una corrida fallida no reemplaza la última publicación; una parcial nunca retira registros.</p>
</section>

<section class="article-section" id="example">
  <h2>Un conjunto verificable</h2>
  <p>La instancia de aceptación de Wazuh reportó 4 agentes (1 activo, 1 desconectado, 2 nunca conectados) y 3 grupos (2 vacíos). Las métricas fueron 4 totales, 1 activo, 0 pendientes, 1 desconectado, 2 nunca conectados, 0 con más de 7 días, 3 grupos y 2 vacíos. Las publicaciones fueron completas, con 4 y 3 registros. Es un ejemplo de laboratorio con datos sintéticos, no un tenant de cliente.</p>
</section>

<section class="article-section" id="not">
  <h2>Qué no está en este catálogo</h2>
  <ul>
    <li><strong>Métricas personalizadas.</strong> Los administradores no pueden definir métricas nuevas en el MVP; llegan con un conector.</li>
    <li><strong>Métricas combinadas entre herramientas.</strong> Un valor pertenece a un conector y una conexión. Los totales CyberArk+Wazuh no están en el MVP.</li>
    <li><strong>Alertas o detecciones de Wazuh.</strong> Las alertas viven en el indexer y este conector no las lee.</li>
    <li><strong>Una población Identity.</strong> El MVP inventaría herramientas y sus métricas; no modela un directorio de personas como fuente de métricas.</li>
    <li><strong>Valores en tiempo real.</strong> Las métricas se leen sobre la última publicación, con su fecha y completitud.</li>
  </ul>
  <p>La página de <a href="/es/limites/">límites actuales</a> registra qué está implementado, probado y aceptado, y qué sigue pendiente.</p>
</section>
`,
    docs: [
      { label: 'Reglas de métricas y contratos de conectores', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#métricas-por-conector-decisión-del-dueño-1611' },
      { label: 'Métricas declaradas de CyberArk', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#métricas-declaradas-de-cyberark-pam' },
      { label: 'Contrato y aceptación de Wazuh', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#tercer-adaptador-wazuh-agentes-y-grupos' },
    ],
    related: [
      { label: 'Qué inventaría Custodia', href: '/es/inventario/' },
      { label: 'CyberArk PAM', href: '/es/conectores/cyberark-pam/' },
      { label: 'Wazuh', href: '/es/conectores/wazuh/' },
      { label: 'Modelo de seguridad', href: '/es/seguridad/' },
    ],
  },

  security: {
    path: '/es/seguridad/',
    title: 'Modelo de seguridad · Qué lee y protege Custodia',
    description: 'Custodia es de solo lectura: no lee contraseñas ni valores de secretos, cifra los secretos de conexión fuera de la base y limita el inventario por permisos de conexión.',
    eyebrow: 'Seguridad',
    h1: 'Qué lee, guarda y protege Custodia',
    headline: 'Qué lee, guarda y protege Custodia',
    lede: 'Custodia es un inventario de solo lectura, no un sistema de registro de secretos. Esta página fija el límite: qué lee, qué nunca lee, cómo protege las credenciales de conexión, quién puede ver los datos y qué queda fuera de su control.',
    crumb: 'Seguridad',
    status: [
      { tone: 'done', label: 'Conectores de solo lectura' },
      { tone: 'done', label: 'Secretos de conexión cifrados' },
      { tone: 'neutral', label: 'El host y root siguen siendo la autoridad' },
    ],
    body: `
<section class="article-section" id="short">
  <h2>La versión corta</h2>
  <dl class="article-defs">
    <div><dt>Lee</dt><dd>Metadatos allowlisted de un conjunto acotado de endpoints. Nunca lee contraseñas de cuentas, valores de secretos, tickets, sesiones ni el payload crudo del proveedor.</dd></div>
    <div><dt>Guarda</dt><dd>Observaciones, corridas, publicaciones, métricas, permisos, configuración y auditoría. Los secretos de conexión se cifran aparte; la base SQLite no está cifrada en reposo.</dd></div>
    <div><dt>Protege</dt><dd>Los secretos de conexión con XChaCha20-Poly1305, una clave maestra fuera de la base y del backup, entrada write-only en la API y aislamiento entre conexiones.</dd></div>
    <div><dt>No protege</dt><dd>La herramienta de origen, los permisos propios de la aplicación, el host o root, la red, el navegador, el endpoint ni un backup que no se proteja.</dd></div>
  </dl>
</section>

<section class="article-section" id="reads">
  <h2>El límite de solo lectura</h2>
  <p>Cada conector es un contrato versionado con una allowlist. Usa GET y consultas POST documentadas de lectura; no llama a endpoints de alta, edición, borrado ni remediación. Un campo fuera de la allowlist se descarta antes de llegar al inventario.</p>
  <ul>
    <li><strong>CyberArk PAM:</strong> Safes, miembros de Safe, metadatos no secretos de cuentas, plataformas, servidores y conectores PSM, y los cruces plataforma↔PSM. Sin contraseñas, valores de secretos, tickets ni sesiones.</li>
    <li><strong>Wazuh:</strong> agentes y grupos desde la API del manager. Sin alertas, contenido del indexer, claves de agentes ni configuración.</li>
    <li><strong>Transporte:</strong> HTTPS verificado, CA adicional por tenant, sin seguir redirects ni paginación cross-origin con credenciales, y respuestas y páginas acotadas.</li>
  </ul>
  <p>Una corrida fallida o parcial falla cerrada en completitud. No inventa ceros, no retira registros ni reemplaza la última publicación. Ver <a href="/es/conectores/">los contratos de conectores</a> y las <a href="/es/inventario/">reglas del inventario</a>.</p>
</section>

<section class="article-section" id="secrets">
  <h2>Secretos de conexión</h2>
  <p>Un secreto pegado en la consola es <strong>write-only</strong>. Se cifra antes de llegar a SQLite con XChaCha20-Poly1305; la API, la auditoría y el worker nunca devuelven el valor. La API sólo informa que hay un secreto configurado y su versión.</p>
  <ul>
    <li><strong>Clave maestra:</strong> 32 bytes aleatorios en <code>master.key</code> (0600), fuera de la base y del backup por defecto.</li>
    <li><strong>Binding:</strong> el cifrado autenticado liga la versión del formato, el grupo de conexión y el campo; un secreto movido a otra conexión o a una versión anterior no descifra.</li>
    <li><strong>Referencias:</strong> <code>env:</code>, <code>file:</code> y <code>secret:</code> siguen disponibles. <code>file:</code> y <code>secret:</code> se resuelven sólo dentro del directorio de secretos configurado, nunca contra una ruta arbitraria del host.</li>
    <li><strong>Aislamiento:</strong> una conexión no puede tomar prestado el secreto cifrado de otra, y cambiar el origen de una conexión con secreto cifrado exige volver a ingresarlo.</li>
    <li><strong>Rotación:</strong> rotar una credencial reemplaza el valor cifrado y sube su versión. Rotar la clave maestra no está en el MVP.</li>
    <li><strong>Backup:</strong> el backup SQLite no incluye <code>master.key</code>. Restaurar en otro host exige copiar esa clave por separado, o los secretos cifrados no se descifran.</li>
  </ul>
</section>

<section class="article-section" id="stores">
  <h2>Qué está cifrado y qué no</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Dato</th><th>Protección</th></tr></thead>
      <tbody>
        <tr><td>Secretos de conexión</td><td>Cifrados con XChaCha20-Poly1305; clave fuera de la base.</td></tr>
        <tr><td>Clave maestra</td><td>Archivo modo 0600, fuera de SQLite y del backup por defecto. Copiala aparte.</td></tr>
        <tr><td>Inventario, corridas, métricas, permisos y auditoría</td><td>Sin cifrado en reposo por parte de Custodia. Protegé el host, el disco y los backups.</td></tr>
        <tr><td>Registros de auditoría</td><td>En SQLite con el resto del estado. No están cifrados en reposo ni prueban integridad contra quien controla la base.</td></tr>
        <tr><td>Transporte</td><td>TLS protege los datos en tránsito hacia la consola y entre Custodia y un conector.</td></tr>
      </tbody>
    </table>
  </div>
  <p>La distinción importa: «Custodia cifra los secretos de conexión» no significa que toda la instalación esté cifrada. El cifrado de disco, el control de acceso al host y la protección de backups siguen siendo responsabilidad del operador.</p>
</section>

<section class="article-section" id="access">
  <h2>Quién puede ver el inventario</h2>
  <ul>
    <li><strong>Permisos por conexión.</strong> Un usuario necesita permiso de lectura para una conexión; una colección no legible se informa como una no configurada, sin revelar su existencia.</li>
    <li><strong>Filtrado antes de unir.</strong> Las consultas de inventario y detalle filtran por las concesiones del llamante antes de calcular o correlacionar. Un usuario sin lectura no ve la conexión ni sus registros.</li>
    <li><strong>Login local.</strong> Las contraseñas se hashean con Argon2id; las sesiones son opacas, revocables y vencen a las 12 horas o a los 30 minutos sin actividad. Las mutaciones usan CSRF y una comprobación exacta de Origin.</li>
    <li><strong>OIDC.</strong> Opcional y configurado desde la consola con prueba y activación explícita; sin fallback silencioso. La identidad es emisor más sujeto, no una coincidencia de correo. El proveedor puede exigir MFA y política de grupos.</li>
    <li><strong>Auditoría.</strong> Los cambios administrativos se auditan; las decisiones de acceso aparecen en el historial cuando el journal de auditoría está habilitado. Las filas nunca contienen secretos.</li>
  </ul>
  <p>Custodia es una instancia autohospedada única, no un SaaS multiinquilino. No hay aislamiento por tenant, facturación ni plano de control alojado. La página de <a href="/es/limites/">límites</a> registra los bordes de acceso y despliegue.</p>
</section>

<section class="article-section" id="host">
  <h2>Host, backup y autoridad</h2>
  <p>Quien controla el host o root controla la instalación: la base, la configuración, la clave maestra y el proceso. Custodia no es una frontera de seguridad contra un host comprometido o un administrador malicioso.</p>
  <p>Protegé el host, mantené la clave maestra separada del backup, ensayá el restore en una instancia descartable y restringí quién puede leer el directorio de estado. Un backup viejo puede reactivar autorizaciones revocadas; tratá las copias como sensibles.</p>
</section>

<section class="article-section" id="limits">
  <h2>Límites actuales</h2>
  <ul>
    <li><strong>CyberArk PAM:</strong> contrato probado contra fixtures; todavía falta un tenant real de cliente.</li>
    <li><strong>Wazuh:</strong> aceptado contra 4.14.10; un manager, sin cluster, sin alertas ni indexer.</li>
    <li><strong>Acceso:</strong> sin MFA propia, sin SCIM ni mapeo de grupos desde el proveedor. Faltan un proveedor OIDC real y la aceptación desplegada del login local con TLS/proxy.</li>
    <li><strong>Secretos:</strong> sin rotación de la clave maestra; el backup excluye la clave; el restore queda en el host.</li>
    <li><strong>Hosting:</strong> una instancia con SQLite embebido; sin SaaS multiinquilino ni aislamiento por tenant.</li>
    <li><strong>Alcance:</strong> inventario de solo lectura; sin remediación, postura de dispositivo, tráfico no HTTP ni métricas combinadas entre herramientas en el MVP.</li>
  </ul>
</section>

<section class="article-section" id="verify">
  <h2>Verificar las afirmaciones</h2>
  <ul>
    <li>Leé el <a href="https://github.com/custodia-project/Custodia/blob/main/docs/ACCESS.md">contrato de acceso</a> para modos, sesiones y primer administrador.</li>
    <li>Leé el <a href="https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#secretos-de-conexión-y-gestión">contrato de secretos</a> y la <a href="https://github.com/custodia-project/Custodia/blob/main/docs/VALIDATION.md#clave-maestra-y-backups-1611">regla de clave maestra y backups</a>.</li>
    <li>Revisá las <a href="/es/metricas/">métricas declaradas</a> y los <a href="/es/conectores/">límites de los conectores</a>.</li>
    <li>Usá la página de <a href="/es/limites/">límites actuales</a> como fuente de verdad de lo aceptado y lo pendiente.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Contrato de acceso y sesiones', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/ACCESS.md' },
      { label: 'Secretos y gestión de conexiones', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#secretos-de-conexión-y-gestión' },
      { label: 'Clave maestra y backups', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/VALIDATION.md#clave-maestra-y-backups-1611' },
    ],
    related: [
      { label: 'Métricas declaradas', href: '/es/metricas/' },
      { label: 'Límites actuales', href: '/es/limites/' },
      { label: 'Conectores', href: '/es/conectores/' },
      { label: 'Instalar', href: '/es/instalar/' },
    ],
  },

  install: {
    path: '/es/instalar/',
    title: 'Instalar Custodia · Linux, systemd y una release firmada',
    description: 'Custodia se instala en Linux x86_64 o arm64 con systemd. Requisitos, flujo de un comando firmado, primer administrador y límites actuales de la release.',
    eyebrow: 'Instalar',
    h1: 'Instalar Custodia en un host Linux',
    headline: 'Instalar Custodia en un host Linux',
    lede: 'Un comando firmado en Linux x86_64 o arm64 con systemd. El instalador está implementado y aceptado en un host systemd descartable; la primera release pública firmada todavía falta.',
    crumb: 'Instalar',
    status: [
      { tone: 'done', label: 'Gates de instalación y actualización aprobados' },
      { tone: 'pending', label: 'Falta la primera release pública' },
      { tone: 'done', label: 'Sólo Linux y systemd' },
    ],
    body: `
<section class="article-section" id="requirements">
  <h2>Requisitos</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th>Sistema operativo</th><td>Linux x86_64 o arm64 con systemd. Docker y otros orquestadores no forman parte de este MVP.</td></tr>
        <tr><th>Acceso</th><td>Root para instalar el servicio, el binario y los archivos TLS.</td></tr>
        <tr><th>Herramientas del host</th><td><code>curl</code>, <code>ssh-keygen</code> (OpenSSH), <code>openssl</code>, <code>tar</code> y <code>sha256sum</code>. El instalador las comprueba antes de descargar nada; vienen en la mayoría de las distribuciones.</td></tr>
        <tr><th>Dirección</th><td>Un nombre de host o IP para la consola, puerto 8443 por defecto. El puerto 443 requiere <code>CAP_NET_BIND_SERVICE</code>.</td></tr>
        <tr><th>TLS</th><td>Tu certificado y clave, o un certificado autofirmado generado por el instalador con el nombre o IP como SAN.</td></tr>
        <tr><th>Red</th><td>HTTPS saliente a <code>custodia.octaviosantacruz.com</code> para leer el catálogo firmado y descargar el paquete.</td></tr>
        <tr><th>Base de datos</th><td>Ninguna que instalar. SQLite está embebido y el estado vive en <code>/var/lib/custodia/state.db</code>.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="command">
  <h2>Instalar con un comando</h2>
  <p class="article-callout"><strong>Todavía no publicado.</strong> La primera release pública firmada, v0.1.0, sigue pendiente. El comando de abajo es el flujo previsto y lo ejercitan los gates locales de bootstrap y actualización, pero no lo uses como instalación de producción hasta que se publiquen la release y su clave de firma.</p>
  <ol>
    <li>Ejecutalo como root en el servidor:
      <pre class="install-code is-primary"><code>curl -fsSL https://custodia.octaviosantacruz.com/install.sh | sudo sh</code></pre>
    </li>
    <li>Detecta la arquitectura, lee el catálogo <code>stable</code> firmado, verifica su firma OpenSSH con la clave de release embebida en el script y recién entonces hace dos preguntas. Cada una trae una respuesta lista; Enter la acepta. Muestra qué va a instalar y no cambia nada hasta que confirmás:
      <pre class="install-preview" tabindex="0" aria-label="Ejemplo de una instalación interactiva"><code><span class="t-dim">Custodia 0.1.0 for linux/amd64 (signed catalog verified).</span>
Console address (how people will open Custodia) [https://custodia.example.com:8443]: <span class="t-in">↵</span>
TLS certificate: 1) self-signed  2) my own certificate files [1]: <span class="t-in">↵</span>

Custodia 0.1.0 will be installed:
  console:     https://custodia.example.com:8443
  certificate: self-signed for this address
  binary:      /usr/local/bin/custodia
  data:        /var/lib/custodia
  service:     custodia.service (systemd)
Continue? [Y/n]: <span class="t-in">↵</span>
<span class="t-dim">…</span>
<span class="t-ok">Custodia 0.1.0 installed.</span>
  binary: /usr/local/bin/custodia
  data:   /var/lib/custodia
  config: /etc/custodia
  URL:    https://custodia.example.com:8443

Finish in your browser: open https://custodia.example.com:8443 and create the first
administrator with this code. It works once and is not stored in clear.
  <span class="t-key">Setup code: KX7M-P4QR</span> (valid until 10:15 UTC, 15 minutes; a new code replaces this one)
Need another code? sudo -u custodia /usr/local/bin/custodia setup-code --dir /var/lib/custodia</code></pre>
    </li>
    <li>Abrí la dirección de la consola, ingresá el código y elegí tu propia contraseña. Esa cuenta es el primer administrador; ver <a href="#first-admin">más abajo</a>.</li>
  </ol>
  <p>Rechaza un catálogo vencido, de otro canal, de otra arquitectura o una vuelta atrás que no se haya permitido explícitamente. Volver a ejecutarlo en un host ya instalado conserva la base y la clave maestra.</p>
</section>

<section class="article-section" id="verify">
  <h2>Revisar el script antes de ejecutarlo</h2>
  <p>Así, por tubería, el script y la clave de release que lleva vienen del mismo sitio, o sea que confiás en su TLS, como con cualquier instalador <code>curl | sh</code>. Para comprobarlo vos, descargá el script y su firma, conseguí la clave de release por otro camino (la copia versionada en el repositorio con la primera release), verificá, leé el script y ejecutá el archivo:</p>
  <pre class="install-code"><code>curl -fsSLO https://custodia.octaviosantacruz.com/install.sh
curl -fsSLO https://custodia.octaviosantacruz.com/install.sh.sig
# allowed_signers: la clave de release, tomada de la copia del repositorio
ssh-keygen -Y verify -f allowed_signers -I custodia-release \\
  -n custodia-installer -s install.sh.sig &lt; install.sh
sudo sh install.sh</code></pre>
  <p>Para que la instalación confíe sólo en la clave que comprobaste, pasala con <code>--allowed-signers ARCHIVO</code>. El archivo debe ser local, no un enlace, no escribible por grupo u otros y, si el instalador corre como root, propiedad de root. El instalador fija la clave que verificó en <code>/usr/local/lib/custodia/allowed_signers</code>; las actualizaciones posteriores aceptan sólo esa clave, así un compromiso posterior de este sitio no alcanza a servidores ya instalados.</p>
  <p>Este sitio también publica <code>/allowed_signers</code> para comparar de un vistazo, pero una copia que viene del mismo lugar que el script no agrega un control independiente.</p>
</section>

<section class="article-section" id="options">
  <h2>Opciones y automatización</h2>
  <p>Sin terminal, o con <code>--yes</code>, no pregunta nada y aplica los valores por defecto. Por tubería, las opciones van después de <code>sh -s --</code>:</p>
  <pre class="install-code"><code>curl -fsSL https://custodia.octaviosantacruz.com/install.sh | sudo sh -s -- \\
  --public-origin https://custodia.example.com:8443 --yes</code></pre>
  <div class="table-wrap">
    <table class="table is-compact is-flags">
      <tbody>
        <tr><th><code>--public-origin URL</code></th><td>Dirección que abre la gente para la consola; empieza con <code>https://</code>. Por defecto: el nombre completo del host, o su primera IP, en el puerto de escucha.</td></tr>
        <tr><th><code>--tls-cert ARCHIVO --tls-key ARCHIVO</code></th><td>Tu certificado y clave en PEM. Sin ellos se crea un autofirmado para la dirección de la consola.</td></tr>
        <tr><th><code>--listen HOST:PUERTO</code></th><td>Dirección en la que escucha el servicio. Por defecto <code>0.0.0.0:8443</code>.</td></tr>
        <tr><th><code>--yes</code></th><td>Acepta los valores por defecto y la confirmación sin preguntar.</td></tr>
        <tr><th><code>--version V</code></th><td>Falla si el catálogo firmado no nombra exactamente esa versión.</td></tr>
        <tr><th><code>--allowed-signers ARCHIVO</code></th><td>Confía en esta clave de release en lugar de la embebida en el script.</td></tr>
        <tr><th><code>--allow-downgrade</code></th><td>Permite instalar una versión más vieja que la instalada.</td></tr>
        <tr><th><code>--uninstall [--purge]</code></th><td>Quita el servicio y el binario. Datos y configuración quedan, salvo que se agregue <code>--purge</code>.</td></tr>
      </tbody>
    </table>
  </div>
  <p><code>sh install.sh --help</code> lista el resto, como <code>--prefix</code>, <code>--data-dir</code>, <code>--config-dir</code> y un <code>--package</code> explícito con su <code>--sha256</code>.</p>
</section>

<section class="article-section" id="what-it-does">
  <h2>Qué hace el instalador</h2>
  <ol>
    <li>Verifica el catálogo firmado y el manifiesto firmado del paquete antes de escribir en el host.</li>
    <li>Crea el usuario de sistema <code>custodia</code>, la unidad de systemd, el directorio de datos y el directorio de configuración propiedad de root.</li>
    <li>Inicializa la base SQLite y la clave maestra de 32 bytes. La clave se mueve a <code>/etc/custodia/master.key</code> con modo 0600 y queda fuera de la base y del backup por defecto.</li>
    <li>Configura TLS con tu certificado o uno autofirmado, e inicia la consola en la dirección y el puerto elegidos.</li>
    <li>Crea el watcher de actualización y el timer de backup diario. Las actualizaciones se verifican, se respaldan y se revierten con un helper root.</li>
    <li>Imprime un código de configuración de un solo uso en lugar de una contraseña. El código vence en 15 minutos; la persona crea el primer administrador en el navegador.</li>
  </ol>
</section>

<section class="article-section" id="layout">
  <h2>Dónde queda cada cosa</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th><code>/usr/local/bin/custodia</code></th><td>El binario; <code>custodia version</code> muestra su versión.</td></tr>
        <tr><th><code>/usr/local/lib/custodia/</code></th><td>La consola web y la clave de release fijada, <code>allowed_signers</code>.</td></tr>
        <tr><th><code>/var/lib/custodia/state.db</code></th><td>La base SQLite, del usuario <code>custodia</code>.</td></tr>
        <tr><th><code>/etc/custodia/</code></th><td>Configuración, propiedad de root y legible por el servicio. Contiene <code>master.key</code> y <code>tls/</code>.</td></tr>
        <tr><th><code>custodia.service</code></th><td>El servicio systemd, más <code>custodia-update.path</code> y <code>custodia-backup.timer</code>.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="first-admin">
  <h2>Primer administrador</h2>
  <p>El instalador no imprime una contraseña. Imprime un código de configuración válido por un solo uso y 15 minutos. Abrí la consola, ingresá el código y elegí tu propia contraseña. El camino de configuración se cierra cuando existe el primer administrador; crear o resetear otros administradores sigue siendo una operación del host hasta que la consola tenga ese recorrido.</p>
  <p>Si el código venció, generá otro en el host; reemplaza al anterior:</p>
  <pre class="install-code"><code>sudo -u custodia /usr/local/bin/custodia setup-code --dir /var/lib/custodia</code></pre>
  <p>El login local es el modo base. OIDC se configura desde la consola con una prueba y una activación explícita; no es un fallback silencioso. El contrato detallado de acceso está enlazado abajo.</p>
</section>

<section class="article-section" id="update">
  <h2>Actualizar y desinstalar</h2>
  <p>Después de instalar, la consola muestra versión, salud, backups, diagnóstico de conectores y la actualización disponible. Una actualización pedida desde ahí la aplica un helper root que verifica el paquete firmado, respalda la base, comprueba la salud y vuelve atrás solo si la versión nueva no responde. Nunca instala una versión más vieja que la que corre.</p>
  <p>Para quitar Custodia, ejecutá el instalador con <code>--uninstall</code>. Detiene y quita las unidades y el binario y conserva datos y configuración; <code>--purge</code> también los borra, incluida la clave maestra:</p>
  <pre class="install-code"><code>curl -fsSL https://custodia.octaviosantacruz.com/install.sh | sudo sh -s -- --uninstall</code></pre>
</section>

<section class="article-section" id="limits">
  <h2>Límites actuales</h2>
  <ul>
    <li>Todavía no hay release pública. La clave de firma y el primer paquete firmado deben publicarse antes de que el comando sea una instalación de producción.</li>
    <li>Sólo Linux y systemd. Sin Docker, Kubernetes ni servicio de Windows en este MVP.</li>
    <li>Sin ACME. Usá un certificado propio y renovalo fuera de Custodia, o conservá el autofirmado para una consola privada.</li>
    <li>Una instancia con SQLite embebido. Sin instalación en cluster.</li>
    <li>El restore queda en el host; la consola crea y descarga backups, pero no los restaura.</li>
    <li>El backup no incluye la clave maestra. Copiala por separado o los secretos cifrados no se descifran en otro host.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Instalación, TLS, releases y layout', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/INSTALL.md' },
      { label: 'Primer administrador con código de configuración', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/ACCESS.md#primer-administrador-con-código-de-configuración-1711' },
      { label: 'Clave maestra y backups', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/VALIDATION.md#clave-maestra-y-backups-1611' },
    ],
    related: [
      { label: 'Límites actuales', href: '/es/limites/' },
      { label: 'Qué inventaría Custodia', href: '/es/inventario/' },
      { label: 'Conectores', href: '/es/conectores/' },
    ],
  },

  limits: {
    path: '/es/limites/',
    title: 'Límites actuales · Qué hace Custodia hoy',
    description: 'Un estado honesto de Custodia: capacidades implementadas, probadas y aceptadas, más los límites de conectores, despliegue y producto que siguen pendientes.',
    eyebrow: 'Límites',
    h1: 'Qué hace Custodia hoy y qué no',
    headline: 'Qué hace Custodia hoy y qué no',
    lede: 'Una página de estado honesta: qué capacidades están implementadas, cuáles se aceptaron contra una instancia real y cuáles siguen pendientes. Sin fechas de release ni promesas de hoja de ruta.',
    crumb: 'Límites',
    status: [
      { tone: 'pending', label: 'MVP en construcción' },
      { tone: 'done', label: 'Solo lectura por diseño' },
      { tone: 'done', label: 'Una sola instancia' },
    ],
    body: `
<section class="article-section" id="labels">
  <h2>Cómo leer el estado</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Etiqueta</th><th>Qué significa</th></tr></thead>
      <tbody>
        <tr><td><span class="tag is-done">Implementado</span></td><td>El código y las pruebas automatizadas existen en el repositorio. No acredita un despliegue de cliente.</td></tr>
        <tr><td><span class="tag is-done">Probado</span></td><td>El camino corrió contra fixtures, un servidor local o una instancia descartable con datos sintéticos.</td></tr>
        <tr><td><span class="tag is-done">Aceptado</span></td><td>El camino corrió contra la herramienta o el host real nombrados, con acceso autorizado, y sus límites quedaron registrados.</td></tr>
        <tr><td><span class="tag is-pending">Pendiente</span></td><td>El trabajo está diseñado o parcialmente construido, pero no pasó la aceptación que necesita.</td></tr>
        <tr><td><span class="tag is-planned">Planificado</span></td><td>El catálogo lo nombra, pero no hay contrato y no se puede configurar ni usar.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="available">
  <h2>Qué está disponible hoy</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Área</th><th>Qué funciona</th><th>Evidencia y límite</th></tr></thead>
      <tbody>
        <tr><td>Acceso</td><td>Login local, sesiones, CSRF, flujo de administrador y OIDC configurable.</td><td>Implementado y probado en laboratorio. Faltan un proveedor OIDC real y la aceptación desplegada con TLS/proxy.</td></tr>
        <tr><td>Secretos de conexión</td><td>Secreto bearer write-only cifrado con XChaCha20-Poly1305; referencias externas; rotación, pausa y retiro.</td><td>Probado, incluso el aislamiento entre conexiones. La clave maestra debe conservarse fuera de la base.</td></tr>
        <tr><td>CyberArk PAM</td><td>Safes, miembros, metadatos no secretos de cuentas, plataformas, catálogos PSM y cruces plataforma↔PSM, todo de solo lectura.</td><td>Contrato probado contra fixtures. Todavía no hay un tenant real de cliente aceptado.</td></tr>
        <tr><td>Wazuh</td><td>Agentes y grupos de solo lectura desde la API del manager.</td><td>Aceptado contra Wazuh 4.14.10 con manager y agente de laboratorio. Un manager; alertas e indexer fuera de alcance.</td></tr>
        <tr><td>Inventario</td><td>Inventario por conexión, completitud de colecciones, métricas declaradas, fechas y detalle de registros.</td><td>Implementado en laboratorio. No hay métricas combinadas entre herramientas en el MVP.</td></tr>
        <tr><td>Operación</td><td>Instalador firmado, TLS, servicio systemd, backups desde la consola, solicitud de actualización y rollback verificado.</td><td>Aceptado en un host systemd descartable. Faltan la primera release pública y su clave de firma.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="pending">
  <h2>Qué sigue pendiente</h2>
  <ul>
    <li><strong>Primera release pública firmada.</strong> El paquete, el instalador, el catálogo y la clave de firma deben publicarse antes de que la instalación de un comando sea para usuarios.</li>
    <li><strong>Un tenant real de CyberArk.</strong> El contrato se probó contra fixtures, no contra una instalación de cliente con credenciales de solo lectura autorizadas.</li>
    <li><strong>Un proveedor OIDC real y un login local desplegado.</strong> Ambos tienen cobertura de laboratorio; faltan un proveedor de producción y el despliegue con TLS/proxy.</li>
    <li><strong>Restore desde la consola.</strong> Los backups se crean y descargan; el restore sigue siendo una operación del host.</li>
    <li><strong>Más conectores.</strong> Fleet, DefectDojo, CrowdSec, OPNsense, Keycloak, MISP y OpenBao son candidatos. Las herramientas comerciales necesitan un tenant de cliente. Ninguna tiene soporte hasta que su contrato esté implementado y probado.</li>
    <li><strong>Cluster y alertas de Wazuh.</strong> Un manager es el contrato actual; las alertas viven en el indexer y no se leen.</li>
    <li><strong>Métricas y cruces entre herramientas.</strong> El MVP mide cada conector por separado. Los cruces necesitan reglas explícitas, evidencia, vigencia y permisos.</li>
    <li><strong>Métricas personalizadas.</strong> Los conectores declaran sus métricas en el binario; los administradores no pueden definir nuevas en el MVP.</li>
    <li><strong>Docker, ACME y SaaS.</strong> Docker y los certificados automáticos quedan fuera del MVP. Custodia no es un SaaS multiinquilino y no implementa facturación ni aislamiento por cliente.</li>
    <li><strong>Rotación de la clave maestra.</strong> Rotar una credencial de conexión reemplaza el secreto cifrado; rotar la clave maestra no está en el MVP.</li>
  </ul>
</section>

<section class="article-section" id="security">
  <h2>Límites de seguridad y operación</h2>
  <ul>
    <li>Sin MFA propia. Un proveedor OIDC puede exigir contraseña, passkey o MFA; Custodia no agrega un segundo factor al login local.</li>
    <li>Sin mapeo de grupos o roles desde el proveedor de identidad. Un administrador aprueba una identidad y concede conexiones explícitas; el correo no se usa para enlazar cuentas.</li>
    <li>Sin SCIM, múltiples emisores ni logout iniciado por el proveedor en el MVP.</li>
    <li>Los backups no incluyen la clave maestra. Restaurar en otro host exige copiar esa clave por separado, o los secretos de conexión cifrados no se descifran.</li>
    <li>La consola crea y descarga backups, pero el restore sigue en el host.</li>
  </ul>
</section>

<section class="article-section" id="not">
  <h2>Qué no es</h2>
  <ul>
    <li>No es un SIEM: no recolecta alertas, no correlaciona detecciones ni reemplaza a Wazuh, QRadar u otro SIEM.</li>
    <li>No es un PAM: no guarda, rota ni entrega contraseñas de CyberArk.</li>
    <li>No es un escáner: no descubre redes, no ejecuta chequeos de vulnerabilidades ni prueba credenciales.</li>
    <li>No es una herramienta de remediación: no cambia, aísla ni reinicia nada en la herramienta de origen.</li>
    <li>No está listo para producción hoy: la primera release pública, el tenant real de CyberArk y la aceptación desplegada del acceso siguen abiertos.</li>
    <li>No es un SaaS: el producto es una instancia autohospedada con SQLite; el aislamiento multiinquilino y la facturación son sólo una evaluación posterior.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Criterios de aceptación y distribución', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/VALIDATION.md' },
      { label: 'Modos de acceso y límites', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/ACCESS.md' },
      { label: 'Estado y catálogo de conectores', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md' },
    ],
    related: [
      { label: 'Instalar Custodia', href: '/es/instalar/' },
      { label: 'Conectores', href: '/es/conectores/' },
      { label: 'Qué inventaría Custodia', href: '/es/inventario/' },
    ],
  },
};
