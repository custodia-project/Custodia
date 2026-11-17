// English pages. Each key has a Spanish pair in articles-es.mjs with the same
// path structure under /es/. Facts come from docs/CONNECTORS.md,
// docs/INSTALL.md, docs/ACCESS.md and docs/VALIDATION.md; do not turn a
// planned connector or a fixture test into an accepted integration.
export const articlesEn = {
  inventory: {
    path: '/inventory/',
    title: 'What Custodia inventories · Security tools and metrics',
    description: 'Custodia keeps a read-only inventory of connected security tools, their collections and declared metrics, with provenance, data date and completeness.',
    eyebrow: 'Inventory',
    h1: 'What Custodia inventories',
    headline: 'What Custodia inventories',
    lede: 'Custodia keeps a read-only inventory of the security tools you connect, the collections each tool exposes, and the metrics its connector declares. Every value keeps its source, data date and whether the run was complete.',
    crumb: 'Inventory',
    status: [
      { tone: 'done', label: 'Implemented in the self-hosted MVP' },
      { tone: 'pending', label: 'First public release pending' },
    ],
    body: `
<section class="article-section" id="definition">
  <h2>An inventory layer, not another console</h2>
  <p>Custodia is not a SIEM, a PAM, a vulnerability scanner or a remediation tool. It reads from tools you already run, keeps what they reported, and shows the numbers their connectors declare. It does not change the source tool.</p>
  <p>The product answers one narrow set of questions well: which tools are connected, what each one reported, how fresh and complete that report is, and which records compose a number. It leaves detection, response, password rotation and ticket management to the tools that own them.</p>
  <dl class="article-defs">
    <div><dt>Observation</dt><dd>One record read from a collection: a Safe, an agent, an account metadata row. It keeps the external ID, the connection, the run and the data date.</dd></div>
    <div><dt>Collection</dt><dd>A bounded set of records declared by a connector, such as <code>safes</code>, <code>agents</code> or <code>groups</code>.</dd></div>
    <div><dt>Publication</dt><dd>The last complete or partial run that made a collection readable. A failed run does not replace the previous publication.</dd></div>
    <div><dt>Metric</dt><dd>A calculation declared by the connector over a published collection, with a versioned ID and a readable explanation.</dd></div>
  </dl>
</section>

<section class="article-section" id="what-it-keeps">
  <h2>What it keeps</h2>
  <p>Every connection is a configured instance of a supported tool. A connection names its URL, credential reference or encrypted secret, CA and selected collections. The inventory keeps them separate: two tenants of the same product are two connections, with their own runs and permissions.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Question</th><th>What the inventory shows</th></tr></thead>
      <tbody>
        <tr><td>Which tools are connected and when did they run?</td><td>Connection, connector, collections, last publication and completeness.</td></tr>
        <tr><td>How many agents, Safes or accounts are there?</td><td>Totals and breakdowns by state, with the date of the data.</td></tr>
        <tr><td>What changed or stopped reporting?</td><td>Age, partiality and unavailable values. Never an invented zero.</td></tr>
        <tr><td>Where does each number come from?</td><td>A drill-down to the exact records that compose the metric and the run behind them.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Human decisions are a separate layer. A classification, an owner or a follow-up note does not overwrite what the tool observed, and a later synchronization does not erase the decision.</p>
</section>

<section class="article-section" id="metrics">
  <h2>How a metric is calculated</h2>
  <p>Each connector declares its metrics in the binary, next to its collections. The administrator does not define them in the MVP, and a metric cannot read the raw provider payload or a field outside the connector allowlist.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Calculation</th><th>Meaning</th></tr></thead>
      <tbody>
        <tr><td><code>count</code></td><td>Active records in the published collection.</td></tr>
        <tr><td><code>count_where</code></td><td>Active records whose allowlisted field equals one of the declared values. A record without the field does not count.</td></tr>
        <tr><td><code>ratio</code></td><td>A percentage with an explicit numerator and denominator, rounded to one decimal.</td></tr>
        <tr><td><code>age_over</code></td><td>Records whose RFC 3339 field is older than a fixed number of days, measured from the publication date.</td></tr>
        <tr><td><code>count_without</code></td><td>Records in one collection that no record in a sibling collection references through the declared field.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Metrics are computed when read over the last publication. There is no metrics table and no materialized value that can drift from the records. A partial run keeps its value and marks it partial; a proportion or a <code>count_without</code> whose denominator or reference is partial is not calculated.</p>
  <p class="article-callout"><strong>No data is not zero.</strong> Without a configured collection or a publication, the metric is “unavailable”. Zero is a value only when a publication confirms that no records match the calculation. Each value carries the data date and the run state of every source behind it.</p>
</section>

<section class="article-section" id="example">
  <h2>A verifiable example</h2>
  <p>The Wazuh connector was accepted against a laboratory manager running Wazuh 4.14.10 with synthetic data. The manager reported four agents and three groups. The connector published both collections completely and produced these values:</p>
  <div class="table-wrap">
    <table class="table is-compact is-fit">
      <thead><tr><th>Metric</th><th>Value</th></tr></thead>
      <tbody>
        <tr><td>Agents total</td><td>4</td></tr>
        <tr><td>Active agents</td><td>1</td></tr>
        <tr><td>Pending agents</td><td>0</td></tr>
        <tr><td>Disconnected agents</td><td>1</td></tr>
        <tr><td>Never-connected agents</td><td>2</td></tr>
        <tr><td>Agents without contact for more than 7 days</td><td>0</td></tr>
        <tr><td>Groups total</td><td>3</td></tr>
        <tr><td>Empty groups</td><td>2</td></tr>
      </tbody>
    </table>
  </div>
  <p>This is an acceptance example on a laboratory instance with synthetic data, not a customer tenant. It shows the contract: the numbers add up to the records, the data date is visible, and the run is complete. A partial page would have kept the previous publication and changed the completeness state instead of pretending the missing records were gone.</p>
</section>

<section class="article-section" id="not">
  <h2>What it does not do</h2>
  <ul>
    <li>It does not remediate, rotate passwords, isolate devices or change records in the source tool.</li>
    <li>It does not combine metrics across tools in the MVP. A number belongs to one connector and one connection.</li>
    <li>It does not replace the source console or its history. It keeps the bounded projection it was allowed to read.</li>
    <li>It does not infer an identity from similar names. Cross-tool links need an explicit rule and evidence.</li>
    <li>It does not read Wazuh alerts or the indexer, or CyberArk account passwords. Those are outside the current contracts.</li>
  </ul>
  <p>Two connectors are implemented: CyberArk PAM and Wazuh. The <a href="/connectors/">connectors page</a> separates implemented contracts from planned catalog entries and explains why a planned tool cannot be configured.</p>
</section>
`,
    docs: [
      { label: 'Connector contracts and metric rules', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md' },
      { label: 'Product scope and principles', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/PRODUCT.md' },
    ],
    related: [
      { label: 'Connectors: what Custodia can read', href: '/connectors/' },
      { label: 'CyberArk PAM: what Custodia reads', href: '/connectors/cyberark-pam/' },
      { label: 'Wazuh: agents and groups', href: '/connectors/wazuh/' },
    ],
  },

  connectors: {
    path: '/connectors/',
    title: 'Connectors · CyberArk PAM, Wazuh and planned tools',
    description: 'Custodia connectors are versioned read-only contracts. CyberArk PAM and Wazuh are implemented; planned tools are not supported and cannot be configured.',
    eyebrow: 'Connectors',
    h1: 'Connectors: what Custodia can read',
    headline: 'Connectors: what Custodia can read',
    lede: 'A connector is a versioned read-only contract for one tool. A connection is one configured instance of that tool. The catalog lists both implemented contracts and planned tools, and the difference matters.',
    crumb: 'Connectors',
    status: [
      { tone: 'done', label: 'Two connectors implemented' },
      { tone: 'pending', label: 'Real CyberArk tenant pending' },
      { tone: 'planned', label: 'Other connectors planned' },
    ],
    body: `
<section class="article-section" id="what-is-a-connector">
  <h2>Three different identities</h2>
  <dl class="article-defs">
    <div><dt>Provider</dt><dd>Who offers the tool: CyberArk, Wazuh, a firewall vendor.</dd></div>
    <div><dt>Connector type</dt><dd>The contract that knows how to read one API. It declares authentication, endpoints, collections, allowlists and metrics.</dd></div>
    <div><dt>Connection</dt><dd>One configured instance: one tenant, URL, credential, CA, scope and schedule. Two tenants of the same product are two connections.</dd></div>
  </dl>
  <p>That separation is why “CyberArk” is not a connection and why adding a second tenant does not copy the first one’s secrets, checkpoints or permissions. A connection belongs to the installation of the operator; Custodia is not a central multi-tenant service.</p>
</section>

<section class="article-section" id="how-a-connection-works">
  <h2>How a connection works</h2>
  <ol>
    <li>Choose an implemented connector and a local name for the connection.</li>
    <li>Enter the HTTPS base URL and a credential reference or a write-only secret. Add a CA if the tool uses a private one.</li>
    <li>Run a probe. It reports connectivity, authentication and permission failures per endpoint; a 200 on one endpoint does not prove the whole connector.</li>
    <li>Select the collections to read. The probe plan covers the union of their endpoints.</li>
    <li>Run the first synchronization. The worker reads only the declared collections and publishes transactionally.</li>
    <li>Review the last complete or partial run, then enable a schedule. Any change to URL, authentication, CA or secret turns the schedule off until a new probe passes.</li>
  </ol>
  <p>Secrets are write-only from the console. A pasted bearer secret is encrypted with XChaCha20-Poly1305 before it reaches SQLite; the API, audit log and worker never return the value. External <code>env:</code>, <code>file:</code> and <code>secret:</code> references remain available. The master key lives outside the database and outside the default backup.</p>
  <p class="article-callout"><strong>Read-only is a contract, not a setting.</strong> The adapter uses GET requests and documented read-only POST queries. It does not call create, update or delete endpoints, and it does not load third-party plugins or scripts.</p>
</section>

<section class="article-section" id="implemented">
  <h2>Implemented today</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Connector</th><th>Collections</th><th>Accepted against</th><th>Still pending</th></tr></thead>
      <tbody>
        <tr>
          <td><a href="/connectors/cyberark-pam/">CyberArk PAM</a><br /><code>cyberark-pam-safes-v1</code></td>
          <td>Safes, Safe members, non-secret account metadata, platforms, PSM servers and connectors, platform↔PSM links.</td>
          <td>Local fixtures that follow the documented CyberArk API shape.</td>
          <td>A real customer tenant with authorized read-only credentials.</td>
        </tr>
        <tr>
          <td><a href="/connectors/wazuh/">Wazuh</a><br /><code>wazuh</code></td>
          <td>Agents and groups.</td>
          <td>Wazuh 4.14.10, manager and agent, on a laboratory instance.</td>
          <td>Cluster support and versions outside the reviewed contract.</td>
        </tr>
      </tbody>
    </table>
  </div>
  <p>The CyberArk row is deliberately precise: the contract and its fixtures exist, but Custodia has not read a real customer tenant. The Wazuh row has a real acceptance against the named version. Neither row grants support for every feature of the vendor product.</p>
</section>

<section class="article-section" id="planned">
  <h2>Planned is not supported</h2>
  <p>The catalog also lists open-source and commercial tools that are not implemented. A planned descriptor declares no endpoints, collections or credentials, and the connection API rejects it. Listing a tool is a direction, not a contract.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Open-source candidates</th><th>Commercial tools, with a customer tenant</th></tr></thead>
      <tbody>
        <tr>
          <td>Fleet, DefectDojo, CrowdSec, OPNsense, Keycloak, MISP, OpenBao.</td>
          <td>Netskope, IBM QRadar, CrowdStrike Falcon, FortiMail, FortiGate, Palo Alto Networks, Tenable.</td>
        </tr>
      </tbody>
    </table>
  </div>
  <p>Each new connector needs its own contract, synthetic fixtures, allowlist, declared metrics and acceptance against an instance the project is allowed to read. The project does not copy vendor code or fixtures without a license review, and it does not invent an API for a tool it has not tested.</p>
</section>

<section class="article-section" id="limits">
  <h2>What a connector does not do</h2>
  <ul>
    <li>It does not write to the source tool or perform remediation.</li>
    <li>It does not normalize every field of every API into one universal model. Each contract has a bounded allowlist.</li>
    <li>It does not merge records from different tools by similar names.</li>
    <li>It does not expose secrets, raw payloads or fields outside the allowlist in the inventory or the drill-down.</li>
    <li>It does not promise a connector that is only listed in the catalog.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Connector contract, catalog and metrics', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md' },
      { label: 'Connection secrets and management', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#secretos-de-conexión-y-gestión' },
    ],
    related: [
      { label: 'What Custodia inventories', href: '/inventory/' },
      { label: 'CyberArk PAM', href: '/connectors/cyberark-pam/' },
      { label: 'Wazuh', href: '/connectors/wazuh/' },
    ],
  },

  'cyberark-pam': {
    path: '/connectors/cyberark-pam/',
    title: 'CyberArk PAM connector · Safes, members, accounts and PSM',
    description: 'Custodia reads bounded CyberArk PAM metadata: Safes, Safe members, non-secret account metadata, platforms and PSM catalogs. Passwords and secrets are never read.',
    eyebrow: 'CyberArk PAM',
    h1: 'CyberArk PAM: what Custodia reads',
    headline: 'CyberArk PAM: what Custodia reads',
    lede: 'Custodia reads a bounded set of CyberArk PAM metadata: Safes, Safe members, non-secret account metadata, platforms and PSM catalogs. It never reads or stores account passwords.',
    crumb: 'CyberArk PAM',
    status: [
      { tone: 'done', label: 'Contract and fixtures tested' },
      { tone: 'pending', label: 'Real customer tenant pending' },
      { tone: 'done', label: 'Read-only' },
    ],
    body: `
<section class="article-section" id="what-it-reads">
  <h2>What it reads</h2>
  <p>The connector is <code>cyberark-pam-safes-v1</code>. It follows the documented shape of the CyberArk PAM REST API and keeps only the fields in its allowlist. A field that is not in the allowlist is discarded before it reaches the inventory.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Collection</th><th>Endpoint</th><th>What is kept</th></tr></thead>
      <tbody>
        <tr><td><code>safes</code></td><td><code>GET /API/Safes</code></td><td>Safe number and name, location, creator, retention, auto-purge, creation and modification times, managing CPM and expiry flag.</td></tr>
        <tr><td><code>safe_members</code></td><td><code>GET /API/Safes/{safeName}/Members</code></td><td>Member identity, type, expiration, predefined/read-only flags and the bounded permission object.</td></tr>
        <tr><td><code>accounts</code></td><td><code>GET /API/Accounts</code></td><td>Non-secret metadata: account ID and name, user, address, Safe, platform, type, category and automatic-management status.</td></tr>
        <tr><td><code>platforms</code></td><td><code>GET /API/Platforms</code></td><td>Platform ID and name, active flag, base platform, type, system type and PSM server reference.</td></tr>
        <tr><td><code>psm_servers</code></td><td><code>GET /API/PSM/Servers</code></td><td>PSM server ID, name and address.</td></tr>
        <tr><td><code>psm_connectors</code></td><td><code>GET /API/PSM/Connectors</code></td><td>PSM connector ID and display name.</td></tr>
        <tr><td><code>platform_psm_links</code></td><td>Computed from platforms and PSM servers</td><td>Exact, case-folded, ambiguous or missing reference between a platform and a PSM server.</td></tr>
      </tbody>
    </table>
  </div>
  <p class="article-callout"><strong>Never read:</strong> account passwords, secret values, tickets, sessions, <code>platformAccountProperties</code>, manual-management reasons, last-modified-by fields or the raw provider payload. Accounts without a visible Safe or a usable ID are skipped; the run is marked partial and does not retire accounts.</p>
</section>

<section class="article-section" id="requirements">
  <h2>Requirements</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th>Base URL</th><td>HTTPS with an optional path prefix such as <code>/PasswordVault</code>. No embedded user, query or fragment.</td></tr>
        <tr><th>Read-only identity</th><td>An account allowed to read the selected collections. Custodia does not need write, rotate or session permissions.</td></tr>
        <tr><th>Authentication</th><td>Bearer token, OAuth client credentials or mTLS. The probe validates the chosen mode; a bearer secret can be stored write-only and encrypted.</td></tr>
        <tr><th>Credential references</th><td><code>env:</code>, <code>file:</code> or <code>secret:</code> references; literal credentials are rejected. <code>file:</code> and <code>secret:</code> resolve only inside the configured secret directory.</td></tr>
        <tr><th>TLS</th><td>The tenant’s CA can be provided as a file. Redirects and pagination links outside the same origin are not followed with credentials.</td></tr>
      </tbody>
    </table>
  </div>
  <p>The connection is created with an HTTPS URL and a credential reference; the probe and the worker use the same bounded plan. A change to URL, authentication, CA or secret invalidates the probe and turns the schedule off.</p>
</section>

<section class="article-section" id="metrics">
  <h2>Declared metrics</h2>
  <p>The connector declares these metrics over its published collections. They are computed on read; none of them reads a secret or the raw payload.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Metric</th><th>ID</th><th>Calculation</th></tr></thead>
      <tbody>
        <tr><td>Safes total</td><td><code>cyberark-pam.safes.total.v1</code></td><td><code>count</code> over <code>safes</code>.</td></tr>
        <tr><td>Safes without members</td><td><code>cyberark-pam.safes.without_members.v1</code></td><td><code>count_without</code> over <code>safes</code> and <code>safe_members</code>. Not calculated if the member collection is partial.</td></tr>
        <tr><td>Accounts total</td><td><code>cyberark-pam.accounts.total.v1</code></td><td><code>count</code> over <code>accounts</code>.</td></tr>
        <tr><td>Accounts with automatic management disabled</td><td><code>cyberark-pam.accounts.auto_management_disabled.v1</code></td><td><code>count_where automatic_management_enabled=false</code>.</td></tr>
        <tr><td>Share of accounts without automatic management</td><td><code>cyberark-pam.accounts.auto_management_disabled_ratio.v1</code></td><td>Ratio over accounts that report <code>true</code> or <code>false</code>; the denominator can be smaller than the account total.</td></tr>
        <tr><td>Accounts with a failed last change</td><td><code>cyberark-pam.accounts.last_change_failed.v1</code></td><td><code>count_where management_status=failure</code>.</td></tr>
        <tr><td>Inactive platforms</td><td><code>cyberark-pam.platforms.inactive.v1</code></td><td><code>count_where active=false</code>.</td></tr>
        <tr><td>Platforms with unresolved PSM</td><td><code>cyberark-pam.platforms.psm_unresolved.v1</code></td><td><code>count_where resolution</code> is <code>missing</code> or <code>ambiguous</code> over <code>platform_psm_links</code>.</td></tr>
      </tbody>
    </table>
  </div>
  <p>The platform↔PSM link is evidence, not a guess. An <code>ambiguous</code> row records why two catalog IDs could not be chosen; it is not an edge. A <code>missing</code> row keeps the raw reference instead of inventing a target.</p>
</section>

<section class="article-section" id="limits">
  <h2>Current limits</h2>
  <ul>
    <li><strong>No real tenant accepted yet.</strong> The adapter is tested against local fixtures that follow the documented API shape. A customer tenant with authorized read-only credentials is still required before calling the integration accepted.</li>
    <li>One connection represents one tenant. Collections under the same connection share URL, credential, CA and authentication.</li>
    <li>Only the listed collections are read. Platform details and <code>AllowedSafes</code> are outside this contract.</li>
    <li>No writes, password rotation, session management or SIA/Risk data in the production build.</li>
    <li>Metrics are per connector. There are no combined CyberArk-plus-Wazuh metrics in the MVP.</li>
    <li>A partial run never retires records; a failed run does not replace the last publication.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'CyberArk PAM contract and fixture tests', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#segundo-adaptador-cyberark-pam-safes-fixture' },
      { label: 'Declared CyberArk metrics', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#métricas-declaradas-de-cyberark-pam' },
    ],
    related: [
      { label: 'Wazuh: agents and groups', href: '/connectors/wazuh/' },
      { label: 'What Custodia inventories', href: '/inventory/' },
      { label: 'Current limits', href: '/limits/' },
    ],
  },

  wazuh: {
    path: '/connectors/wazuh/',
    title: 'Wazuh connector · Agents and groups, not alerts',
    description: 'Custodia reads Wazuh agents and groups through the manager API. Accepted against Wazuh 4.14.10; alerts, the indexer and cluster support are outside this contract.',
    eyebrow: 'Wazuh',
    h1: 'Wazuh: agents and groups, not alerts',
    headline: 'Wazuh: agents and groups, not alerts',
    lede: 'Custodia reads the Wazuh manager API to inventory agents and groups. It does not read the indexer or alerts, and it does not change agents.',
    crumb: 'Wazuh',
    status: [
      { tone: 'done', label: 'Accepted against Wazuh 4.14.10' },
      { tone: 'pending', label: 'One manager only' },
      { tone: 'neutral', label: 'Alerts and indexer out of scope' },
    ],
    body: `
<section class="article-section" id="what-it-reads">
  <h2>What it reads</h2>
  <p>The connector logs in to the manager API with Basic authentication, receives a JWT valid for 900 seconds and keeps it only in memory for the probe or the run. It never persists the token and never refreshes it; a run that outlives the token fails closed and is retried.</p>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Call</th><th>Use</th><th>What is kept</th></tr></thead>
      <tbody>
        <tr><td><code>POST /security/user/authenticate</code></td><td>Basic authentication</td><td>No token is stored; it lives only in memory for the operation.</td></tr>
        <tr><td><code>GET /manager/info</code></td><td>Probe only</td><td>Manager identity for the access check.</td></tr>
        <tr><td><code>GET /agents</code></td><td><code>agents</code> collection</td><td>External ID, name, status, IPs, operating system, agent version, groups, group count, last keepalive, registration and manager/node.</td></tr>
        <tr><td><code>GET /groups</code></td><td><code>groups</code> collection</td><td>Group name and agent count.</td></tr>
      </tbody>
    </table>
  </div>
  <p>Agent configuration, keys, alert data and the full provider payload are not part of the allowlist. The API paginates with <code>limit</code>, <code>offset</code> and <code>sort</code>; the connector checks the reported total and fails if a page or the count is inconsistent.</p>
</section>

<section class="article-section" id="requirements">
  <h2>Requirements</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th>Wazuh version</th><td>4.14.10 is the accepted version. Another major version needs a contract review before it is declared available.</td></tr>
        <tr><th>API endpoint</th><td>The manager API over HTTPS, usually port 55000, with a CA file if the certificate is private.</td></tr>
        <tr><th>Read-only user</th><td>An API user with <code>agent:read</code>, <code>group:read</code> and <code>manager:read</code>. Creating an agent must be denied.</td></tr>
        <tr><th>Scope</th><td>One manager. Cluster nodes are not combined in this contract.</td></tr>
        <tr><th>Writes</th><td>None. Custodia does not create, restart or remove agents, and it does not change group membership.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="metrics">
  <h2>Declared metrics</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Metric</th><th>Calculation</th></tr></thead>
      <tbody>
        <tr><td>Agents total</td><td><code>count</code> over <code>agents</code>.</td></tr>
        <tr><td>Active agents</td><td><code>count_where status=active</code>.</td></tr>
        <tr><td>Pending agents</td><td><code>count_where status=pending</code>.</td></tr>
        <tr><td>Disconnected agents</td><td><code>count_where status=disconnected</code>.</td></tr>
        <tr><td>Never-connected agents</td><td><code>count_where status=never_connected</code>.</td></tr>
        <tr><td>Agents without contact for more than 7 days</td><td><code>age_over</code> on <code>last_keep_alive</code> with a fixed 7-day threshold. An agent without keepalive is not counted as old.</td></tr>
        <tr><td>Groups total</td><td><code>count</code> over <code>groups</code>.</td></tr>
        <tr><td>Empty groups</td><td><code>count_where agent_count=0</code>.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="acceptance">
  <h2>Acceptance example</h2>
  <p>The connector was accepted on 16 November 2026 against a laboratory manager and agent, both Wazuh 4.14.10, with synthetic data. The manager reported four agents and three groups. The probe had zero failed endpoints; the publications were complete with four and three records.</p>
  <div class="table-wrap">
    <table class="table is-compact is-fit">
      <thead><tr><th>Metric</th><th>Value</th></tr></thead>
      <tbody>
        <tr><td>Agents total</td><td>4</td></tr>
        <tr><td>Active agents</td><td>1</td></tr>
        <tr><td>Pending agents</td><td>0</td></tr>
        <tr><td>Disconnected agents</td><td>1</td></tr>
        <tr><td>Never-connected agents</td><td>2</td></tr>
        <tr><td>Agents without contact for more than 7 days</td><td>0</td></tr>
        <tr><td>Groups total</td><td>3</td></tr>
        <tr><td>Empty groups</td><td>2</td></tr>
      </tbody>
    </table>
  </div>
  <p>This is a laboratory acceptance, not a customer tenant. The connection, encrypted secret, CA, probe, schedule and inventory were exercised from the console with the same code path used by the product.</p>
</section>

<section class="article-section" id="limits">
  <h2>Current limits</h2>
  <ul>
    <li>One manager; cluster support is not implemented.</li>
    <li>No alerts and no indexer. Wazuh alert data lives outside this API and is not read or invented.</li>
    <li>No writes or agent actions. Custodia only reads.</li>
    <li>The JWT is renewed per probe or run; it is not persisted.</li>
    <li>Versions outside the reviewed 4.14.10 contract need a new review.</li>
    <li>No combined Wazuh-plus-CyberArk metrics in the MVP.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Wazuh contract, endpoints and acceptance', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md#tercer-adaptador-wazuh-agentes-y-grupos' },
      { label: 'Wazuh 4.14.10 release notes', href: 'https://documentation.wazuh.com/current/release-notes/release-4-14-10.html' },
      { label: 'Wazuh API reference', href: 'https://documentation.wazuh.com/current/user-manual/api/reference.html' },
    ],
    related: [
      { label: 'CyberArk PAM: what Custodia reads', href: '/connectors/cyberark-pam/' },
      { label: 'What Custodia inventories', href: '/inventory/' },
      { label: 'Current limits', href: '/limits/' },
    ],
  },

  install: {
    path: '/install/',
    title: 'Install Custodia · Linux, systemd and a signed release',
    description: 'Custodia installs on Linux x86_64 or arm64 with systemd. Requirements, the signed one-command flow, first administrator and current release limits.',
    eyebrow: 'Install',
    h1: 'Install Custodia on a Linux host',
    headline: 'Install Custodia on a Linux host',
    lede: 'One signed command on Linux x86_64 or arm64 with systemd. The installer is implemented and accepted on a disposable systemd host; the first public signed release is still pending.',
    crumb: 'Install',
    status: [
      { tone: 'done', label: 'Installer and update gates passed' },
      { tone: 'pending', label: 'First public release pending' },
      { tone: 'done', label: 'Linux and systemd only' },
    ],
    body: `
<section class="article-section" id="requirements">
  <h2>Requirements</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <tbody>
        <tr><th>Operating system</th><td>Linux x86_64 or arm64 with systemd. Docker and other orchestrators are not part of this MVP.</td></tr>
        <tr><th>Access</th><td>Root to install the service, the binary and the TLS files.</td></tr>
        <tr><th>Tools on the host</th><td><code>curl</code> and OpenSSH (<code>ssh-keygen</code>) for signature verification. Both are preinstalled on most distributions.</td></tr>
        <tr><th>Address</th><td>A host name or IP for the console, port 8443 by default. Port 443 needs <code>CAP_NET_BIND_SERVICE</code>.</td></tr>
        <tr><th>TLS</th><td>Your own certificate and key, or a self-signed certificate generated by the installer with the host name or IP as SAN.</td></tr>
        <tr><th>Network</th><td>Outbound HTTPS to <code>custodia.octaviosantacruz.com</code> to read the signed catalog and download the package.</td></tr>
        <tr><th>Database</th><td>None to install. SQLite is embedded and the state lives in <code>/var/lib/custodia/state.db</code>.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="command">
  <h2>The command</h2>
  <p class="article-callout"><strong>Not published yet.</strong> The first public signed release, v0.1.0, is still pending. The command below is the intended flow and is exercised by the local bootstrap and update gates, but do not use it as a production installation until the release and its signing key are published.</p>
  <pre class="install-code"><code>curl -fsSL https://custodia.octaviosantacruz.com/install.sh | sudo sh</code></pre>
  <p>Without arguments, the installer detects the architecture, reads the signed <code>stable</code> channel for that architecture, verifies its OpenSSH signature with the release key embedded in the script, downloads the package it names and checks its SHA-256. It refuses an expired catalog, another channel, another architecture or a downgrade that was not explicitly allowed.</p>
  <p>If you prefer to inspect the script first, download it with its signature and verify it with the published release key:</p>
  <pre class="install-code"><code>curl -fsSLO https://custodia.octaviosantacruz.com/install.sh
curl -fsSLO https://custodia.octaviosantacruz.com/install.sh.sig
curl -fsSLO https://custodia.octaviosantacruz.com/allowed_signers
ssh-keygen -Y verify -f allowed_signers -I custodia-release \\
  -n custodia-installer -s install.sh.sig &lt; install.sh</code></pre>
  <p>With the first release, the public key will also be versioned in the repository as a comparison copy. Until that release exists, there is no published signer to compare; the script is not ready for a production install.</p>
</section>

<section class="article-section" id="what-it-does">
  <h2>What the installer does</h2>
  <ol>
    <li>Verifies the signed catalog and the signed package manifest before writing to the host.</li>
    <li>Creates the <code>custodia</code> system user, the systemd unit, the data directory and the root-owned configuration directory.</li>
    <li>Initializes the SQLite database and the 32-byte master key. The key is moved to <code>/etc/custodia/master.key</code> with mode 0600 and stays outside the database and the default backup.</li>
    <li>Configures TLS with your certificate or a self-signed one, and starts the console on the chosen address and port.</li>
    <li>Creates the update watcher and the daily backup timer. Updates are verified, backed up and rolled back by a root helper.</li>
    <li>Prints a one-use setup code instead of a password. The code expires in 15 minutes; the person creates the first administrator in the browser.</li>
  </ol>
  <p>After installation, the console shows the version, health, backups, connector diagnostics and update request. A new code can be generated on the host with <code>sudo -u custodia custodia setup-code --dir /var/lib/custodia</code>.</p>
</section>

<section class="article-section" id="first-admin">
  <h2>First administrator</h2>
  <p>The installer does not print a password. It prints a setup code valid for one use and 15 minutes. Open the console, enter the code and choose your own password. The setup path closes after the first administrator exists; creating or resetting other administrators stays on the host until the console grows that workflow.</p>
  <p>Local login is the base mode. OIDC can be configured from the console with a test and an explicit activation; it is not a silent fallback. The detailed access contract is linked below.</p>
</section>

<section class="article-section" id="limits">
  <h2>Current limits</h2>
  <ul>
    <li>No public release yet. The signing key and the first signed package must be published before the command is a production install.</li>
    <li>Linux and systemd only. No Docker, Kubernetes or Windows service in this MVP.</li>
    <li>No ACME. Use a certificate you provide and renew it outside Custodia, or keep the self-signed certificate for a private console.</li>
    <li>One instance with embedded SQLite. No clustered installation.</li>
    <li>Restore stays on the host; the console can create and download backups but does not restore them.</li>
    <li>The backup does not include the master key. Copy it separately, or encrypted secrets will not decrypt on another host.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Installation, TLS, releases and layout', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/INSTALL.md' },
      { label: 'First administrator with a setup code', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/ACCESS.md#primer-administrador-con-código-de-configuración-1711' },
      { label: 'Master key and backups', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/VALIDATION.md#clave-maestra-y-backups-1611' },
    ],
    related: [
      { label: 'Current limits', href: '/limits/' },
      { label: 'What Custodia inventories', href: '/inventory/' },
      { label: 'Connectors', href: '/connectors/' },
    ],
  },

  limits: {
    path: '/limits/',
    title: 'Current limits · What Custodia does today',
    description: 'An honest status of Custodia: implemented, tested and accepted capabilities, plus the connector, deployment and product limits that are still pending.',
    eyebrow: 'Limits',
    h1: 'What Custodia does today, and what it does not',
    headline: 'What Custodia does today, and what it does not',
    lede: 'An honest status page: which capabilities are implemented, which have been accepted against a real instance, and which are still pending. No release dates and no roadmap promises.',
    crumb: 'Limits',
    status: [
      { tone: 'pending', label: 'MVP in progress' },
      { tone: 'done', label: 'Read-only by design' },
      { tone: 'done', label: 'Single instance' },
    ],
    body: `
<section class="article-section" id="labels">
  <h2>How to read the status</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Label</th><th>What it means</th></tr></thead>
      <tbody>
        <tr><td><span class="tag is-done">Implemented</span></td><td>Code and automated tests exist in the repository. It does not prove a customer deployment.</td></tr>
        <tr><td><span class="tag is-done">Tested</span></td><td>The path ran against fixtures, a local server or a disposable instance with synthetic data.</td></tr>
        <tr><td><span class="tag is-done">Accepted</span></td><td>The path ran against the named real tool or host, with authorized access, and its limits are recorded.</td></tr>
        <tr><td><span class="tag is-pending">Pending</span></td><td>The work is designed or partially built but has not passed the acceptance it needs.</td></tr>
        <tr><td><span class="tag is-planned">Planned</span></td><td>The catalog names it, but there is no contract and it cannot be configured or used.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="available">
  <h2>What is available now</h2>
  <div class="table-wrap">
    <table class="table is-compact">
      <thead><tr><th>Area</th><th>What works</th><th>Evidence and limit</th></tr></thead>
      <tbody>
        <tr><td>Access</td><td>Local login, sessions, CSRF, administrator flow and configurable OIDC.</td><td>Implemented and tested in the laboratory. A real OIDC provider and a deployed TLS/proxy acceptance are still pending.</td></tr>
        <tr><td>Connection secrets</td><td>Write-only bearer secret encrypted with XChaCha20-Poly1305; external references; rotation, pause and retirement.</td><td>Tested, including cross-connection isolation. The master key must be preserved outside the database.</td></tr>
        <tr><td>CyberArk PAM</td><td>Read-only Safes, members, non-secret account metadata, platforms, PSM catalogs and platform↔PSM links.</td><td>Contract tested against fixtures. No real customer tenant accepted yet.</td></tr>
        <tr><td>Wazuh</td><td>Read-only agents and groups from the manager API.</td><td>Accepted against Wazuh 4.14.10 with a laboratory manager and agent. One manager; alerts and indexer are out of scope.</td></tr>
        <tr><td>Inventory</td><td>Per-connection inventory, collection completeness, declared metrics, data dates and record drill-down.</td><td>Implemented in the laboratory. There are no cross-tool combined metrics in the MVP.</td></tr>
        <tr><td>Operations</td><td>Signed installer, TLS, systemd service, console backups, update request and verified rollback.</td><td>Accepted on a disposable systemd host. The first public release and its signing key are pending.</td></tr>
      </tbody>
    </table>
  </div>
</section>

<section class="article-section" id="pending">
  <h2>What is still pending</h2>
  <ul>
    <li><strong>First public signed release.</strong> The package, installer, catalog and signing key must be published before the one-command install is for users.</li>
    <li><strong>A real CyberArk tenant.</strong> The contract is tested against fixtures, not a customer installation with authorized read-only credentials.</li>
    <li><strong>A real OIDC provider and a deployed local login.</strong> Both have laboratory coverage; a production provider and TLS/proxy deployment are still pending.</li>
    <li><strong>Restore from the console.</strong> Backups can be created and downloaded; restore remains a host operation.</li>
    <li><strong>More connectors.</strong> Fleet, DefectDojo, CrowdSec, OPNsense, Keycloak, MISP and OpenBao are candidates. Commercial tools need a customer tenant. None is supported until its contract is implemented and tested.</li>
    <li><strong>Wazuh cluster and alerts.</strong> One manager is the current contract; alerts live in the indexer and are not read.</li>
    <li><strong>Cross-tool metrics and inferences.</strong> The MVP measures each connector separately. Crosses need explicit rules, evidence, freshness and permissions.</li>
    <li><strong>Custom metrics.</strong> Connectors declare their metrics in the binary; administrators cannot define new ones in the MVP.</li>
    <li><strong>Docker, ACME and SaaS.</strong> Docker and automatic certificates are outside the MVP. Custodia is not a multi-tenant SaaS and does not implement billing or tenant isolation.</li>
    <li><strong>Master key rotation.</strong> Rolling a connection credential replaces the encrypted secret; rotating the master key is not in the MVP.</li>
  </ul>
</section>

<section class="article-section" id="security">
  <h2>Security and operations limits</h2>
  <ul>
    <li>No MFA of its own. An OIDC provider can enforce password, passkey or MFA policies; Custodia does not add a second factor to local login.</li>
    <li>No group or role mapping from the identity provider. An administrator approves an identity and grants explicit connections; email is not used to link accounts.</li>
    <li>No SCIM, multiple issuers or provider-initiated logout in the MVP.</li>
    <li>Backups exclude the master key. Restoring on another host requires copying that key separately, or encrypted connection secrets will not decrypt.</li>
    <li>The console creates and downloads backups, but restore stays on the host.</li>
  </ul>
</section>

<section class="article-section" id="not">
  <h2>What it is not</h2>
  <ul>
    <li>Not a SIEM: it does not collect alerts, correlate detections or replace Wazuh, QRadar or another SIEM.</li>
    <li>Not a PAM: it does not store, rotate or check out CyberArk passwords.</li>
    <li>Not a scanner: it does not discover networks, run vulnerability checks or test credentials.</li>
    <li>Not a remediation tool: it does not change, isolate or restart anything in the source tool.</li>
    <li>Not production-ready today: the first public release, the real CyberArk tenant and the deployed access acceptance are still open.</li>
    <li>Not a SaaS: the product is a single self-hosted instance with SQLite; multi-tenant isolation and billing are only a later evaluation.</li>
  </ul>
</section>
`,
    docs: [
      { label: 'Acceptance criteria and distribution', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/VALIDATION.md' },
      { label: 'Access modes and limits', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/ACCESS.md' },
      { label: 'Connector status and catalog', href: 'https://github.com/custodia-project/Custodia/blob/main/docs/CONNECTORS.md' },
    ],
    related: [
      { label: 'Install Custodia', href: '/install/' },
      { label: 'Connectors', href: '/connectors/' },
      { label: 'What Custodia inventories', href: '/inventory/' },
    ],
  },
};
