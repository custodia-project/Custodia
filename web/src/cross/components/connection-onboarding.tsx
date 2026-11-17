import { useCallback, useEffect, useMemo, useState } from 'react';
import { Pause, Pencil, Play, PlugZap, RotateCcw, ShieldCheck, Trash2 } from 'lucide-react';
import { ApiError } from '../lib/api';
import { createConnection, fetchConnectionGroups, fetchConnectors, probeConnection, rotateConnectionSecret, setConnectionState, updateConnectionGroup } from '../lib/ingestion-api';
import {
  buildConnectionRequest, connectableConnectors, emptyConnectionForm,
  type ConnectionForm,
} from '../lib/connection-onboarding';
import type { ConnectionGroup, ConnectorDescriptor, ConnectionProbe, TenantConnection } from '../types/ingestion';
import { StateBlock } from './state-block';
import { StatusBadge } from './semantic';
import { t } from '../i18n/index.ts';
import './connection-onboarding.css';
import './user-admin.css';

const AUTH_LABEL: Record<string, string> = {
  bearer: 'Bearer token',
  'oauth-client-credentials': 'OAuth client credentials',
  'basic-exchange': 'Usuario y contraseña (JWT)',
  mtls: 'mTLS',
};

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return <label className="connection-field">
    <span>{label}</span>
    {children}
    {hint && <small>{hint}</small>}
  </label>;
}

export function ConnectionOnboarding({ permissions, onUnauthorized }: { permissions: string[]; onUnauthorized?: () => void }) {
  const isAdmin = permissions.includes('installation:manage');
  const [connectors, setConnectors] = useState<ConnectorDescriptor[]>([]);
  const [form, setForm] = useState<ConnectionForm>(emptyConnectionForm());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<TenantConnection | null>(null);
  const [probe, setProbe] = useState<ConnectionProbe | null>(null);
  const [groups, setGroups] = useState<ConnectionGroup[]>([]);
  const [groupBusy, setGroupBusy] = useState('');
  const [notice, setNotice] = useState('');
  const [rotateFor, setRotateFor] = useState<ConnectionGroup | null>(null);
  const [rotateSecret, setRotateSecret] = useState('');
  const [editFor, setEditFor] = useState<ConnectionGroup | null>(null);
  const [editBaseUrl, setEditBaseUrl] = useState('');
  const [editInterval, setEditInterval] = useState('0');
  const [editSecret, setEditSecret] = useState('');
  const [retireFor, setRetireFor] = useState<ConnectionGroup | null>(null);

  const available = useMemo(() => connectableConnectors(connectors), [connectors]);
  const descriptor = available.find((row) => row.id === form.connector);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const rows = await fetchConnectors(signal);
      if (signal?.aborted) return;
      setConnectors(rows);
      const visible = await fetchConnectionGroups(signal).catch(() => [] as ConnectionGroup[]);
      if (signal?.aborted) return;
      setGroups(visible);
      setLoading(false);
    } catch (caught) {
      if (signal?.aborted) return;
      if (caught instanceof ApiError && caught.status === 401) { onUnauthorized?.(); return; }
      setError(caught instanceof Error ? caught.message : t('No se pudo cargar el catálogo de conectores'));
      setLoading(false);
    }
  }, [onUnauthorized]);

  useEffect(() => {
  if (!isAdmin) { setLoading(false); return; }
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [isAdmin, load]);

  async function act(group: ConnectionGroup, action: 'pause' | 'resume' | 'retire') {
  setGroupBusy(group.group_id);
  setError('');
  try {
    await setConnectionState(group.group_id, action);
    setNotice(action === 'pause' ? t('Conexión pausada; la agenda quedó apagada.') : action === 'resume' ? t('Conexión reanudada.') : t('Conexión retirada; el historial se conserva.'));
    setRetireFor(null);
    await load();
  } catch (caught) {
    setError(caught instanceof Error ? caught.message : t('No se pudo cambiar la conexión'));
  } finally {
    setGroupBusy('');
  }
}
async function runGroupProbe(group: ConnectionGroup) {
  setGroupBusy(group.group_id);
  setError('');
  try {
    const result = await probeConnection(group.group_id);
    setNotice(result.failed_endpoints === 0 ? t('Acceso correcto; la agenda ya puede activarse.') : t('{count} endpoints con error. Revisá el detalle en la corrida.', { count: result.failed_endpoints }));
    await load();
  } catch (caught) {
    setError(caught instanceof Error ? caught.message : t('No se pudo probar el acceso'));
  } finally {
    setGroupBusy('');
  }
}
async function rotate() {
  if (!rotateFor) return;
  setGroupBusy(rotateFor.group_id);
  setError('');
  try {
    await rotateConnectionSecret(rotateFor.group_id, rotateSecret);
    setNotice(t('Secreto rotado y cifrado. Probá la conexión antes de activar la agenda.'));
    setRotateFor(null);
    setRotateSecret('');
    await load();
  } catch (caught) {
    setError(caught instanceof Error ? caught.message : t('No se pudo rotar el secreto'));
  } finally {
    setGroupBusy('');
  }
}
async function saveEdit() {
  if (!editFor) return;
  setGroupBusy(editFor.group_id);
  setError('');
  try {
    await updateConnectionGroup(editFor.group_id, { base_url: editBaseUrl.trim(), interval_minutes: Number(editInterval), ...(editSecret ? { secret: editSecret } : {}) });
    setNotice(t('Conexión actualizada.'));
    setEditFor(null);
    await load();
  } catch (caught) {
    setError(caught instanceof Error ? caught.message : t('No se pudo actualizar la conexión'));
  } finally {
    setGroupBusy('');
  }
}

  function update(patch: Partial<ConnectionForm>) {
    setForm((current) => ({ ...current, ...patch }));
  }

  function chooseConnector(id: string) {
    setCreated(null);
    setProbe(null);
    setFieldErrors([]);
    setForm(emptyConnectionForm(available.find((row) => row.id === id)));
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!descriptor) return;
    const draft = buildConnectionRequest(form, descriptor);
    if (!draft.ok) { setFieldErrors(draft.errors); return; }
    setFieldErrors([]);
    setError('');
    setBusy(true);
    setProbe(null);
    try {
      const connection = await createConnection(draft.request);
      setCreated(connection);
      setNotice(t('Conexión registrada. Probala antes de activar la agenda.'));
      await load();
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) { onUnauthorized?.(); return; }
      setError(caught instanceof Error ? caught.message : t('No se pudo conectar el tenant'));
    } finally {
      setBusy(false);
    }
  }

  async function runProbe() {
    if (!created) return;
    setBusy(true);
    setError('');
    try {
      setProbe(await probeConnection(created.group_id ?? created.connection_id));
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) { onUnauthorized?.(); return; }
      setError(caught instanceof Error ? caught.message : t('No se pudo probar el acceso'));
    } finally {
      setBusy(false);
    }
  }

  if (!isAdmin) {
    return <section className="activity-panel connection-onboarding">
      <div className="activity-meta"><span><PlugZap size={14} /> {t('Conectar herramienta')}</span></div>
      <StateBlock variant="empty" title={t('Sólo un administrador puede conectar un tenant')} hint={t('El alta guarda únicamente referencias de credencial y queda auditada.')} />
    </section>;
  }

  return <section className="activity-panel connection-onboarding">
    <div className="activity-meta">
      <span><PlugZap size={14} /> {t('Conectar herramienta')}</span>
      <strong>{available.length} {t('herramientas implementadas')}</strong>
    </div>
    <p className="operational-errors-note">{t('Custodia arranca vacía: elegí la herramienta, pegá la URL del tenant y una referencia de credencial. El alta no contacta el tenant; probá el acceso antes de recolectar.')}</p>

    {loading ? <StateBlock variant="loading" title={t('Cargando conectores…')} />
      : error && !connectors.length ? <StateBlock variant="error" title={t('No se pudo cargar el catálogo')} hint={error} />
        : <form className="connection-form" onSubmit={submit}>
          <div className="connection-grid">
            <Field label={t('Herramienta')}>
              <select aria-label={t('Herramienta')} value={form.connector} onChange={(event) => chooseConnector(event.target.value)}>
                <option value="">{t('Elegí una herramienta')}</option>
                {available.map((row) => <option key={row.id} value={row.id}>{row.name} · {row.vendor}</option>)}
              </select>
            </Field>
            <Field label={t('URL del tenant')} hint={t('HTTPS; se admite un prefijo de path como /PasswordVault.')}>
              <input aria-label={t('URL del tenant')} type="url" placeholder="https://pvwa.empresa.example/PasswordVault" value={form.baseUrl} onChange={(event) => update({ baseUrl: event.target.value })} />
            </Field>
            <fieldset className="connection-field connection-collections">
              <legend>{t('Colecciones')}</legend>
              <div className="connection-checks">
                {(descriptor?.collections ?? []).map((collection) => {
                  const checked = form.collections.includes(collection);
                  return <label key={collection} className="connection-check">
                    <input type="checkbox" checked={checked} onChange={(event) => update({
                      collections: event.target.checked
                        ? [...form.collections, collection]
                        : form.collections.filter((item) => item !== collection),
                    })} />
                    <span>{collection}</span>
                  </label>;
                })}
              </div>
              <small>{t('Una conexión recolecta todas las colecciones elegidas con la misma URL y credencial.')}</small>
              <div className="connection-check-actions">
                <button type="button" className="button subtle" onClick={() => update({ collections: [...(descriptor?.collections ?? [])] })}>{t('Todas')}</button>
                <button type="button" className="button subtle" onClick={() => update({ collections: [] })}>{t('Ninguna')}</button>
              </div>
            </fieldset>
            <Field label={t('Alcance')} hint={t('Identifica la línea base; un cambio de alcance no retira registros.')}>
              <input aria-label={t('Alcance')} value={form.scope} onChange={(event) => update({ scope: event.target.value })} placeholder="pam-safes-prod" />
            </Field>
            <Field label={t('Autenticación')}>
              <select aria-label={t('Autenticación')} value={form.authMode} onChange={(event) => update({ authMode: event.target.value })}>
                {(descriptor?.auth_modes ?? []).map((mode) => <option key={mode} value={mode}>{AUTH_LABEL[mode] ?? mode}</option>)}
              </select>
            </Field>
            <Field label={t('Intervalo')} hint={t('0 deshabilita la agenda; mínimo 5 minutos.')}>
              <input aria-label={t('Intervalo')} type="number" min="0" step="5" value={form.interval} onChange={(event) => update({ interval: event.target.value })} />
            </Field>
            {form.authMode === 'basic-exchange' && <Field label={t('Usuario API')} hint={t('Usuario de sólo lectura con permisos agent:read, group:read y manager:read.')}>
              <input aria-label={t('Usuario API')} autoComplete="off" value={form.authUser} onChange={(event) => update({ authUser: event.target.value })} placeholder="custodia-reader" />
            </Field>}
            {form.authMode === 'basic-exchange' && <Field label={t('Referencia de contraseña')} hint={t('Alternativa al secreto: env:NAME, file:/ruta o secret:NAME.')}>
              <input aria-label={t('Referencia de contraseña')} value={form.credentialRef} onChange={(event) => update({ credentialRef: event.target.value, secret: '' })} placeholder="secret:wazuh-reader" />
            </Field>}
            {form.authMode === 'bearer' && <Field label={t('Referencia de credencial')} hint={t('env:NAME, file:/ruta o secret:NAME. Nunca el valor.')}>
              <input aria-label={t('Referencia de credencial')} value={form.credentialRef} onChange={(event) => update({ credentialRef: event.target.value })} placeholder="secret:pam-bearer" />
            </Field>}
            {(form.authMode === 'bearer' || form.authMode === 'basic-exchange') && <Field label={t('Secreto (se guarda cifrado)')} hint={t('Write-only; no se vuelve a mostrar. Dejá la referencia vacía si cargás el secreto.')}>
              <input aria-label={t('Secreto')} type="password" autoComplete="new-password" value={form.secret} onChange={(event) => update({ secret: event.target.value, credentialRef: '' })} placeholder={t('Secreto del tenant')} />
            </Field>}
            {form.authMode === 'oauth-client-credentials' && <>
              <Field label={t('Endpoint de token')}>
                <input aria-label={t('Endpoint de token')} type="url" value={form.tokenUrl} onChange={(event) => update({ tokenUrl: event.target.value })} placeholder="https://pvwa.empresa.example/oauth2/platformtoken" />
              </Field>
              <Field label={t('Client ID (referencia)')}>
                <input aria-label={t('Client ID (referencia)')} value={form.clientIdRef} onChange={(event) => update({ clientIdRef: event.target.value })} placeholder="env:CUSTODIA_PAM_CLIENT_ID" />
              </Field>
              <Field label={t('Client secret (referencia)')}>
                <input aria-label={t('Client secret (referencia)')} value={form.clientSecretRef} onChange={(event) => update({ clientSecretRef: event.target.value })} placeholder="secret:pam-client-secret" />
              </Field>
              <Field label={t('Scopes')} hint={t('Separados por espacio o coma; deben estar autorizados por el tenant.')}>
                <input aria-label={t('Scopes')} value={form.scopes} onChange={(event) => update({ scopes: event.target.value })} placeholder="pam.read" />
              </Field>
            </>}
            {form.authMode === 'mtls' && <>
              <Field label={t('Certificado de cliente')}>
                <input aria-label={t('Certificado de cliente')} value={form.clientCertFile} onChange={(event) => update({ clientCertFile: event.target.value })} placeholder="/etc/custodia/cliente.pem" />
              </Field>
              <Field label={t('Clave de cliente')}>
                <input aria-label={t('Clave de cliente')} value={form.clientKeyFile} onChange={(event) => update({ clientKeyFile: event.target.value })} placeholder="/etc/custodia/cliente.key" />
              </Field>
            </>}
            <Field label={t('CA adicional (opcional)')}>
              <input aria-label={t('CA adicional')} value={form.caFile} onChange={(event) => update({ caFile: event.target.value })} placeholder="/etc/custodia/empresa-ca.pem" />
            </Field>
          </div>
          {descriptor?.notes && <p className="connection-note">{descriptor.notes}</p>}
          {fieldErrors.length > 0 && <ul className="connection-errors" role="alert">{fieldErrors.map((message) => <li key={message}>{message}</li>)}</ul>}
          {error && <p className="sync-schedule-error" role="alert">{error}</p>}
          <div className="connection-actions">
            <button className="button" type="submit" disabled={!descriptor || busy}><ShieldCheck size={14} /> {busy ? t('Guardando…') : t('Conectar tenant')}</button>
            <span className="connection-hint">{t('El secreto viaja una vez, se cifra en SQLite y nunca se devuelve.')}</span>
          </div>
        </form>}

    {created && <div className="connection-created">
      <p role="status">{t('Conexión {id} registrada para {collections}.', { id: created.group_id ?? created.connection_id, collections: (created.collections ?? [created.collection]).join(', ') })}</p>
      <div className="connection-actions">
        <button className="button subtle" type="button" onClick={() => void runProbe()} disabled={busy}><PlugZap size={14} /> {busy ? t('Probando…') : t('Probar acceso')}</button>
        <span className="connection-hint">{t('Sólo lectura; no publica observaciones ni encola una corrida.')}</span>
      </div>
      {probe && <div className="connection-probe">
        <div className="activity-meta"><span>{t('Resultado del probe')}</span><StatusBadge tone={probe.failed_endpoints === 0 ? 'success' : 'warning'}>{probe.failed_endpoints === 0 ? t('Acceso correcto') : t('{count} endpoints con error', { count: probe.failed_endpoints })}</StatusBadge></div>
        <table className="connection-probe-table"><thead><tr><th>{t('Endpoint')}</th><th>{t('Estado')}</th><th>{t('Objetos')}</th><th>{t('Duración')}</th><th>{t('Detalle')}</th></tr></thead>
          <tbody>{probe.report.endpoints.map((row) => <tr key={row.endpoint}>
            <th scope="row"><code>{row.endpoint}</code></th>
            <td>{row.status || '—'}</td>
            <td>{row.objects}</td>
            <td>{row.duration_ms} ms</td>
            <td>{row.error ?? 'ok'}</td>
          </tr>)}</tbody></table>
      </div>}
    </div>}

    {notice && <p className="connection-hint" role="status">{notice}</p>}
    {groups.length > 0 && <div className="connection-groups">
      <div className="activity-meta"><span>{t('Conexiones registradas')}</span><strong>{groups.length}</strong></div>
      <div className="connection-groups-scroll"><table className="connection-groups-table">
        <caption className="sr-only">{t('Conexiones de herramientas')}</caption>
        <thead><tr><th scope="col">{t('Conexión')}</th><th scope="col">{t('Autenticación')}</th><th scope="col">{t('Secreto')}</th><th scope="col">{t('Probe')}</th><th scope="col">{t('Agenda')}</th><th scope="col">{t('Estado')}</th><th scope="col"><span className="sr-only">{t('Acciones')}</span></th></tr></thead>
        <tbody>{groups.map((group) => <tr className="person-row" key={group.group_id}>
          <td><div className="connection-row-identity"><strong>{group.group_id}</strong><span>{group.connector} · {group.collections.join(', ')}</span></div></td>
          <td>{group.auth_mode}</td>
          <td>{group.secret_configured ? t('Cifrado v{version}', { version: group.secret_version }) : group.credential_configured ? t('Referencia externa') : t('Sin credencial')}</td>
          <td>{group.last_probe ? (group.last_probe.ok ? t('Correcta') : t('Con errores')) : t('Sin probar')}</td>
          <td>{group.interval_minutes > 0 ? t('Cada {minutes} min', { minutes: group.interval_minutes }) : t('Apagada')}</td>
          <td><span className={`user-status-chip ${group.retired_at ? 'inactive' : group.enabled ? 'active' : 'pending'}`}>{group.retired_at ? t('Retirada') : group.enabled ? t('Activa') : t('Pausada')}</span></td>
          <td><div className="connection-row-actions">
            {!group.retired_at && <>
              <button className="button subtle" type="button" disabled={!!groupBusy} onClick={() => void runGroupProbe(group)}><PlugZap size={14} /> {t('Probar')}</button>
              <button className="button subtle" type="button" disabled={!!groupBusy} onClick={() => { setEditFor(group); setEditBaseUrl(group.base_url); setEditInterval(String(group.interval_minutes)); setEditSecret(''); }}><Pencil size={14} /> {t('Editar')}</button>
              {group.secret_configured && <button className="button subtle" type="button" disabled={!!groupBusy} onClick={() => { setRotateFor(group); setRotateSecret(''); }}><RotateCcw size={14} /> {t('Rotar secreto')}</button>}
              <button className="button subtle" type="button" disabled={!!groupBusy} onClick={() => void act(group, group.enabled ? 'pause' : 'resume')}>{group.enabled ? <><Pause size={14} /> {t('Pausar')}</> : <><Play size={14} /> {t('Reanudar')}</>}</button>
              <button className="button subtle" type="button" disabled={!!groupBusy} onClick={() => setRetireFor(group)}><Trash2 size={14} /> {t('Retirar')}</button>
            </>}
          </div></td>
        </tr>)}</tbody>
      </table></div>
    </div>}

    {rotateFor && <div className="user-detail-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !groupBusy) setRotateFor(null); }}>
      <div className="user-access-drawer" role="dialog" aria-modal="true" aria-labelledby="rotate-secret-title">
        <header className="user-access-drawer-header"><div><p className="eyebrow">{t('SECRETO DE CONEXIÓN')}</p><h2 id="rotate-secret-title">{t('Rotar secreto')}</h2></div><button className="icon-button" type="button" aria-label={t('Cerrar rotación')} disabled={!!groupBusy} onClick={() => setRotateFor(null)}>×</button></header>
        <div className="connection-form"><Field label={t('Nuevo secreto')} hint={t('Se cifra y no se vuelve a mostrar. La agenda se apaga hasta un probe correcto.')}><input aria-label={t('Nuevo secreto')} type="password" autoComplete="new-password" value={rotateSecret} onChange={(event) => setRotateSecret(event.target.value)} /></Field>
          <div className="connection-actions"><button className="button primary" type="button" disabled={!rotateSecret || !!groupBusy} onClick={() => void rotate()}>{t('Rotar secreto')}</button></div></div>
      </div>
    </div>}

    {editFor && <div className="user-detail-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !groupBusy) setEditFor(null); }}>
      <div className="user-access-drawer" role="dialog" aria-modal="true" aria-labelledby="edit-connection-title">
        <header className="user-access-drawer-header"><div><p className="eyebrow">{t('CONEXIÓN')}</p><h2 id="edit-connection-title">{t('Editar conexión')}</h2></div><button className="icon-button" type="button" aria-label={t('Cerrar edición')} disabled={!!groupBusy} onClick={() => setEditFor(null)}>×</button></header>
        <div className="connection-form">
          <Field label={t('URL del tenant')}><input aria-label={t('URL del tenant')} type="url" value={editBaseUrl} onChange={(event) => setEditBaseUrl(event.target.value)} /></Field>
          <Field label={t('Intervalo')} hint={t('Sólo se activa con un probe correcto.').toString()}><input aria-label={t('Intervalo')} type="number" min="0" step="5" value={editInterval} onChange={(event) => setEditInterval(event.target.value)} /></Field>
          {editFor.secret_configured && !sameOrigin(editFor.base_url, editBaseUrl.trim()) && <Field label={t('Secreto para el nuevo servidor')} hint={t('Cambiar el servidor exige volver a cargar el secreto; el anterior no se envía a otro host.')}><input aria-label={t('Secreto para el nuevo servidor')} type="password" autoComplete="new-password" value={editSecret} onChange={(event) => setEditSecret(event.target.value)} /></Field>}
          <div className="connection-actions"><button className="button primary" type="button" disabled={!editBaseUrl.trim() || !!groupBusy || (editFor.secret_configured && !sameOrigin(editFor.base_url, editBaseUrl.trim()) && !editSecret)} onClick={() => void saveEdit()}>{t('Guardar cambios')}</button></div>
        </div>
      </div>
    </div>}

    {retireFor && <div className="user-detail-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !groupBusy) setRetireFor(null); }}>
      <div className="user-access-drawer" role="alertdialog" aria-modal="true" aria-labelledby="retire-connection-title">
        <header className="user-access-drawer-header"><div><p className="eyebrow">{t('CONEXIÓN')}</p><h2 id="retire-connection-title">{t('Retirar conexión')}</h2></div><button className="icon-button" type="button" aria-label={t('Cerrar retiro')} disabled={!!groupBusy} onClick={() => setRetireFor(null)}>×</button></header>
        <div className="connection-form"><p>{t('Se apaga la agenda y se oculta la conexión; el historial y la auditoría se conservan.')}</p>
          <div className="connection-actions"><button className="button primary" type="button" disabled={!!groupBusy} onClick={() => void act(retireFor, 'retire')}>{t('Retirar conexión')}</button><button className="button subtle" type="button" disabled={!!groupBusy} onClick={() => setRetireFor(null)}>{t('Cancelar')}</button></div></div>
      </div>
    </div>}
  </section>;
}

/** Scheme, host and port of two URLs; an unparsable URL never matches. */
function sameOrigin(left: string, right: string): boolean {
  try {
    return new URL(left).origin === new URL(right).origin;
  } catch {
    return false;
  }
}
