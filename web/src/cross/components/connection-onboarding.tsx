import { useCallback, useEffect, useMemo, useState } from 'react';
import { PlugZap, ShieldCheck } from 'lucide-react';
import { ApiError } from '../lib/api';
import { createConnection, fetchConnectors, probeConnection } from '../lib/ingestion-api';
import {
  buildConnectionRequest, connectableConnectors, emptyConnectionForm,
  type ConnectionForm,
} from '../lib/connection-onboarding';
import type { ConnectorDescriptor, ConnectionProbe, TenantConnection } from '../types/ingestion';
import { StateBlock } from './state-block';
import { StatusBadge } from './semantic';
import { t } from '../i18n/index.ts';
import './connection-onboarding.css';

const AUTH_LABEL: Record<string, string> = {
  bearer: 'Bearer token',
  'oauth-client-credentials': 'OAuth client credentials',
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

  const available = useMemo(() => connectableConnectors(connectors), [connectors]);
  const descriptor = available.find((row) => row.id === form.connector);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const rows = await fetchConnectors(signal);
      if (signal?.aborted) return;
      setConnectors(rows);
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
            {form.authMode === 'bearer' && <Field label={t('Referencia de credencial')} hint={t('env:NAME, file:/ruta o secret:NAME. Nunca el valor.')}>
              <input aria-label={t('Referencia de credencial')} value={form.credentialRef} onChange={(event) => update({ credentialRef: event.target.value })} placeholder="secret:pam-bearer" />
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
            <span className="connection-hint">{t('No se envía ninguna credencial al servidor: sólo la referencia.')}</span>
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
  </section>;
}
