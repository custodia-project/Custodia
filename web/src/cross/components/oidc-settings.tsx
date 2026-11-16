import { useCallback, useEffect, useState } from 'react';
import { CheckCircle2, KeyRound, PlayCircle, Plus, ShieldCheck, ShieldOff, UserRoundX, X } from 'lucide-react';
import { api } from '../lib/api';
import { DateTime } from './date-time';
import './oidc-settings.css';
import { t } from '../i18n/index.ts';

type OIDCConfig = {
  issuer: string;
  client_id: string;
  client_secret_ref: string;
  redirect_uri: string;
  scopes: string;
  subject_claim: string;
  email_claim: string;
  name_claim: string;
  enabled: boolean;
  tested_at: string | null;
  test_ok: boolean;
  test_detail: string;
  updated_at: string;
  updated_by: string;
};

type PendingIdentity = {
  issuer: string;
  subject: string;
  email: string;
  display_name: string;
  first_seen: string;
  last_seen: string;
};

type OIDCUser = {
  issuer: string;
  subject: string;
  display_name: string;
  email: string;
  enabled: boolean;
  last_login_at: string | null;
  active_sessions: number;
  connections: string[];
};

type Connection = { id: string; kind: string; connector?: string };

type Props = {
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

const emptyForm = () => ({
  issuer: '',
  client_id: '',
  client_secret_ref: '',
  redirect_uri: `${window.location.origin}/api/auth/oidc/callback`,
  scopes: 'openid profile email',
  subject_claim: 'sub',
  email_claim: 'email',
  name_claim: 'name',
});

/** Console for the explicit OIDC mode: save a draft, test discovery and keys,
 *  then activate. Pending identities never become principals until an
 *  administrator approves them with a subset of its own connections. */
export function OIDCSettings({ onNotice, onError }: Props) {
  const [config, setConfig] = useState<OIDCConfig | null>(null);
  const [healthy, setHealthy] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const [pending, setPending] = useState<PendingIdentity[]>([]);
  const [accounts, setAccounts] = useState<OIDCUser[]>([]);
  const [connections, setConnections] = useState<Connection[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [busy, setBusy] = useState('');
  const [testResult, setTestResult] = useState<{ ok: boolean; detail: string } | null>(null);
  const [approving, setApproving] = useState<PendingIdentity | null>(null);
  const [grantSelection, setGrantSelection] = useState<string[]>([]);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    try {
      const [current, waiting, approved, available] = await Promise.all([
        api<{ config: OIDCConfig | null; healthy?: boolean }>('/access/oidc'),
        api<{ rows: PendingIdentity[] }>('/access/oidc/pending').catch(() => ({ rows: [] as PendingIdentity[] })),
        api<{ rows: OIDCUser[] }>('/access/oidc/users').catch(() => ({ rows: [] as OIDCUser[] })),
        api<{ rows: Connection[] }>('/connections').catch(() => ({ rows: [] as Connection[] })),
      ]);
      setConfig(current.config);
      setHealthy(current.healthy === true);
      setPending(waiting.rows || []);
      setAccounts(approved.rows || []);
      setConnections(available.rows || []);
      if (current.config) {
        setForm({
          issuer: current.config.issuer,
          client_id: current.config.client_id,
          client_secret_ref: current.config.client_secret_ref,
          redirect_uri: current.config.redirect_uri,
          scopes: current.config.scopes,
          subject_claim: current.config.subject_claim,
          email_claim: current.config.email_claim,
          name_claim: current.config.name_claim,
        });
      }
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : t('No se pudo cargar la configuración OIDC'));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const persist = async () => {
    setBusy('save');
    setTestResult(null);
    try {
      await api('/access/oidc', { method: 'PUT', body: JSON.stringify(form) });
      onNotice(t('Configuración OIDC guardada como borrador. Probala antes de activarla.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo guardar la configuración OIDC'));
    } finally {
      setBusy('');
    }
  };
  const test = async () => {
    setBusy('test');
    try {
      const result = await api<{ ok: boolean; detail: string }>('/access/oidc/test', { method: 'POST', body: '{}' });
      setTestResult(result);
      if (result.ok) onNotice(t('Descubrimiento y claves del proveedor verificados.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo probar el proveedor'));
    } finally {
      setBusy('');
    }
  };
  const setEnabled = async (enabled: boolean) => {
    setBusy(enabled ? 'enable' : 'disable');
    try {
      await api(`/access/oidc/${enabled ? 'enable' : 'disable'}`, { method: 'POST', body: '{}' });
      onNotice(enabled ? t('Proveedor OIDC activado.') : t('Proveedor OIDC desactivado.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo cambiar el estado del proveedor'));
    } finally {
      setBusy('');
    }
  };
  const approve = async () => {
    if (!approving) return;
    setBusy('approve');
    try {
      await api('/access/oidc/pending/approve', {
        method: 'POST',
        body: JSON.stringify({ issuer: approving.issuer, subject: approving.subject, connections: grantSelection }),
      });
      onNotice(t('Identidad OIDC aprobada con las conexiones elegidas.'));
      setApproving(null);
      setGrantSelection([]);
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo aprobar la identidad'));
    } finally {
      setBusy('');
    }
  };
  const deny = async (identity: PendingIdentity) => {
    setBusy(`deny-${identity.subject}`);
    try {
      await api('/access/oidc/pending/deny', {
        method: 'POST',
        body: JSON.stringify({ issuer: identity.issuer, subject: identity.subject }),
      });
      onNotice(t('Solicitud de identidad rechazada.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo rechazar la identidad'));
    } finally {
      setBusy('');
    }
  };
  const act = async (account: OIDCUser, action: 'disable' | 'enable' | 'revoke-sessions') => {
    setBusy(`${action}-${account.subject}`);
    try {
      await api(`/access/oidc/users/${action}`, {
        method: 'POST',
        body: JSON.stringify({ issuer: account.issuer, subject: account.subject }),
      });
      onNotice(action === 'disable' ? t('Cuenta OIDC deshabilitada y sesiones revocadas.') : action === 'enable' ? t('Cuenta OIDC habilitada.') : t('Sesiones OIDC revocadas.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo cambiar la cuenta OIDC'));
    } finally {
      setBusy('');
    }
  };
  const toggleGrant = (id: string) => {
    setGrantSelection((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  };

  const status = !config
    ? { tone: 'pending', label: t('Sin configurar') }
    : config.enabled
      ? { tone: 'active', label: t('Activo') }
      : config.tested_at
        ? { tone: config.test_ok ? 'ready' : 'inactive', label: config.test_ok ? t('Probado') : t('Prueba fallida') }
        : { tone: 'pending', label: t('Borrador') };

  return <section className="oidc-settings">
    <header className="section-heading">
      <div>
        <p className="eyebrow">{t('PROVEEDOR DE IDENTIDAD')}</p>
        <h2>{t('Acceso OIDC')}</h2>
        <span className="database-caption">{t('Custodia actúa como cliente OIDC. El proveedor valida la identidad; Custodia mantiene la sesión, los permisos y la auditoría.')}</span>
      </div>
      <span className={`oidc-status ${status.tone}`}>{status.label}</span>
    </header>
    {loading ? <p className="user-access-note">{t('Cargando configuración...')}</p> : loadError ? <p className="user-directory-error" role="alert">{loadError} <button className="button subtle" type="button" onClick={() => void load()}>{t('Reintentar')}</button></p> : <>
      <form className="oidc-form" onSubmit={(event) => { event.preventDefault(); void persist(); }}>
        <label className="oidc-field wide">{t('Emisor (issuer)')}<input value={form.issuer} placeholder="https://idp.empresa.example" autoComplete="off" onChange={(event) => setForm({ ...form, issuer: event.target.value })} required /></label>
        <label className="oidc-field">{t('Client ID')}<input value={form.client_id} autoComplete="off" onChange={(event) => setForm({ ...form, client_id: event.target.value })} required /></label>
        <label className="oidc-field">{t('Referencia del secreto')}<input value={form.client_secret_ref} placeholder="env:CUSTODIA_OIDC_SECRET" autoComplete="off" onChange={(event) => setForm({ ...form, client_secret_ref: event.target.value })} /></label>
        <label className="oidc-field wide">{t('URL de retorno')}<input value={form.redirect_uri} readOnly /></label>
        <label className="oidc-field wide">{t('Scopes')}<input value={form.scopes} autoComplete="off" onChange={(event) => setForm({ ...form, scopes: event.target.value })} /></label>
        <label className="oidc-field">{t('Claim de sujeto')}<input value={form.subject_claim} autoComplete="off" onChange={(event) => setForm({ ...form, subject_claim: event.target.value })} /></label>
        <label className="oidc-field">{t('Claim de email')}<input value={form.email_claim} autoComplete="off" onChange={(event) => setForm({ ...form, email_claim: event.target.value })} /></label>
        <label className="oidc-field">{t('Claim de nombre')}<input value={form.name_claim} autoComplete="off" onChange={(event) => setForm({ ...form, name_claim: event.target.value })} /></label>
        <p className="oidc-note wide">{t('El secreto se lee de una referencia env:, file: o secret:; su valor nunca se guarda ni se muestra. La identidad se guarda como emisor + sujeto, nunca sólo por email.')}</p>
        <div className="oidc-actions wide">
          <button className="button primary" type="button" disabled={busy === 'save'} onClick={() => void persist()}>{busy === 'save' ? t('Guardando...') : t('Guardar borrador')}</button>
          <button className="button subtle" type="button" disabled={!config || busy === 'test'} onClick={() => void test()}><PlayCircle size={15} /> {busy === 'test' ? t('Probando...') : t('Probar configuración')}</button>
          {config?.enabled
            ? <button className="button subtle" type="button" disabled={busy === 'disable'} onClick={() => void setEnabled(false)}><ShieldOff size={15} /> {t('Desactivar')}</button>
            : <button className="button primary" type="button" disabled={!healthy || busy === 'enable'} onClick={() => void setEnabled(true)}><ShieldCheck size={15} /> {t('Activar proveedor')}</button>}
        </div>
        {testResult ? <p className={`oidc-test ${testResult.ok ? 'ok' : 'failed'}`} role="status">{testResult.ok ? <CheckCircle2 size={15} /> : null}{testResult.detail}</p> : config?.test_detail ? <p className={`oidc-test ${config.test_ok ? 'ok' : 'failed'}`} role="status">{config.test_ok ? <CheckCircle2 size={15} /> : null}{config.test_detail}</p> : null}
        {!healthy && config && !config.enabled ? <p className="oidc-note wide">{t('La activación exige una prueba correcta de la configuración actual, hecha en los últimos diez minutos.')}</p> : null}
      </form>

      <section className="oidc-block">
        <header><div><h3>{t('Identidades pendientes')}</h3><p className="database-caption">{t('Una identidad validada por el proveedor queda acá hasta que un administrador la apruebe. No obtiene permisos antes.')}</p></div><span className="database-toolbar-count">{pending.length}</span></header>
        {pending.length === 0 ? <p className="user-access-note">{t('No hay identidades pendientes.')}</p> : <div className="user-directory-scroll"><table className="user-directory-table">
          <caption className="sr-only">{t('Identidades OIDC pendientes de aprobación')}</caption>
          <thead><tr><th scope="col">{t('Sujeto')}</th><th scope="col">{t('Nombre')}</th><th scope="col">{t('Email')}</th><th scope="col">{t('Último intento')}</th><th scope="col"><span className="sr-only">{t('Acciones')}</span></th></tr></thead>
          <tbody>{pending.map((identity) => <tr className="person-row" key={`${identity.issuer}:${identity.subject}`}>
            <td><div className="user-row-identity"><strong>{identity.display_name || identity.subject}</strong><span>{identity.subject}</span></div></td>
            <td>{identity.display_name}</td>
            <td>{identity.email || t('Sin email')}</td>
            <td><DateTime value={identity.last_seen} /></td>
            <td><div className="user-access-drawer-actions">
              <button className="button subtle" type="button" disabled={!!busy} onClick={() => { setApproving(identity); setGrantSelection([]); }}><Plus size={14} /> {t('Aprobar')}</button>
              <button className="button subtle" type="button" disabled={busy === `deny-${identity.subject}`} onClick={() => void deny(identity)}><X size={14} /> {t('Rechazar')}</button>
            </div></td>
          </tr>)}</tbody>
        </table></div>}
      </section>

      <section className="oidc-block">
        <header><div><h3>{t('Cuentas OIDC aprobadas')}</h3><p className="database-caption">{t('Habilitar o deshabilitar una cuenta revoca sus sesiones. Las concesiones se fijan al aprobarla.')}</p></div><span className="database-toolbar-count">{accounts.length}</span></header>
        {accounts.length === 0 ? <p className="user-access-note">{t('Todavía no hay cuentas OIDC aprobadas.')}</p> : <div className="user-directory-scroll"><table className="user-directory-table">
          <caption className="sr-only">{t('Cuentas OIDC aprobadas')}</caption>
          <thead><tr><th scope="col">{t('Sujeto')}</th><th scope="col">{t('Email')}</th><th scope="col">{t('Estado')}</th><th scope="col">{t('Último acceso')}</th><th scope="col">{t('Sesiones')}</th><th scope="col">{t('Conexiones')}</th><th scope="col"><span className="sr-only">{t('Acciones')}</span></th></tr></thead>
          <tbody>{accounts.map((account) => <tr className="person-row" key={`${account.issuer}:${account.subject}`}>
            <td><div className="user-row-identity"><strong>{account.display_name || account.subject}</strong><span>{account.subject}</span></div></td>
            <td>{account.email || t('Sin email')}</td>
            <td><span className={`user-status-chip ${account.enabled ? 'active' : 'inactive'}`}>{account.enabled ? t('Activo') : t('Deshabilitado')}</span></td>
            <td><DateTime value={account.last_login_at} /></td>
            <td>{account.active_sessions}</td>
            <td>{account.connections.length ? account.connections.join(', ') : t('Sin conexiones')}</td>
            <td><div className="user-access-drawer-actions">
              <button className="button subtle" type="button" disabled={!!busy} onClick={() => void act(account, account.enabled ? 'disable' : 'enable')}>{account.enabled ? <><UserRoundX size={14} /> {t('Deshabilitar')}</> : t('Habilitar')}</button>
              {account.active_sessions > 0 && <button className="button subtle" type="button" disabled={!!busy} onClick={() => void act(account, 'revoke-sessions')}><KeyRound size={14} /> {t('Revocar sesiones')}</button>}
            </div></td>
          </tr>)}</tbody>
        </table></div>}
      </section>
    </>}
    {approving && <div className="user-detail-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) setApproving(null); }}>
      <div className="user-access-drawer" role="dialog" aria-modal="true" aria-labelledby="approve-oidc-title">
        <header className="user-access-drawer-header"><div><p className="eyebrow">{t('IDENTIDAD OIDC')}</p><h2 id="approve-oidc-title">{t('Aprobar identidad')}</h2></div><button className="icon-button" type="button" aria-label={t('Cerrar aprobación')} disabled={!!busy} onClick={() => setApproving(null)}><X size={16} /></button></header>
        <div className="oidc-approve">
          <dl><div><dt>{t('Sujeto')}</dt><dd>{approving.subject}</dd></div><div><dt>{t('Email')}</dt><dd>{approving.email || t('Sin email')}</dd></div><div><dt>{t('Emisor')}</dt><dd>{approving.issuer}</dd></div></dl>
          <div className="user-access-drawer-section"><strong>{t('Conexiones concedidas')}</strong>{connections.length === 0 ? <p className="database-caption">{t('No hay conexiones disponibles para conceder.')}</p> : connections.map((connection) => <label key={connection.id}><input type="checkbox" checked={grantSelection.includes(connection.id)} onChange={() => toggleGrant(connection.id)} /> {connection.id}</label>)}</div>
          <p className="database-caption">{t('Sólo podés conceder conexiones que ya podés leer.')}</p>
          <button className="button primary" type="button" disabled={busy === 'approve'} onClick={() => void approve()}>{busy === 'approve' ? t('Aprobando...') : t('Aprobar identidad')}</button>
        </div>
      </div>
    </div>}
  </section>;
}
