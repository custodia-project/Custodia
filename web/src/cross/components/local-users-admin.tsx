import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { Copy, KeyRound, Plus, ShieldCheck, UserRoundX, X } from 'lucide-react';
import { api } from '../lib/api';
import { generatePassword } from '../lib/password-policy';
import { DateTime } from './date-time';
import './user-admin.css';
import { t } from '../i18n/index.ts';

/** A local account as returned by GET /api/access/users. */
type LocalUser = {
  username: string;
  display_name: string;
  enabled: boolean;
  administrator: boolean;
  must_change_password: boolean;
  last_login_at: string | null;
  active_sessions: number;
  connections: string[];
};

type Connection = { id: string; kind: string; connector?: string };

type Props = {
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/** Minimal local-mode administration: list, create, enable/disable and revoke
 *  sessions through /api/access/users. Grants stay per connection; this view
 *  does not edit them. */
export function LocalUsersAdmin({ onNotice, onError }: Props) {
  const [users, setUsers] = useState<LocalUser[]>([]);
  const [connections, setConnections] = useState<Connection[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [busyUser, setBusyUser] = useState('');
  const [form, setForm] = useState({ username: '', display_name: '', password: '', connections: [] as string[] });
  const [created, setCreated] = useState<{ username: string; password: string } | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    try {
      const [list, available] = await Promise.all([
        api<{ rows: LocalUser[] }>('/access/users'),
        api<{ rows: Connection[] }>('/connections').catch(() => ({ rows: [] as Connection[] })),
      ]);
      setUsers(list.rows || []);
      setConnections(available.rows || []);
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : t('No se pudieron cargar los usuarios'));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const openCreate = () => {
    setForm({ username: '', display_name: '', password: generatePassword(), connections: [] });
    setCreated(null);
    setCreateOpen(true);
  };
  const create = async (event: FormEvent) => {
    event.preventDefault();
    if (!form.username.trim() || !form.display_name.trim() || !form.password) {
      onError(t('Completá usuario, nombre y contraseña temporal.'));
      return;
    }
    setBusy(true);
    try {
      await api('/access/users', {
        method: 'POST',
        body: JSON.stringify({ ...form, username: form.username.trim(), display_name: form.display_name.trim() }),
      });
      setCreated({ username: form.username.trim(), password: form.password });
      setForm({ username: '', display_name: '', password: '', connections: [] });
      setCreateOpen(false);
      onNotice(t('Usuario local creado. Entregá la contraseña temporal fuera de banda.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo crear el usuario'));
    } finally {
      setBusy(false);
    }
  };
  const act = async (user: LocalUser, action: 'disable' | 'enable' | 'revoke-sessions') => {
    setBusyUser(user.username);
    try {
      await api(`/access/users/${action}`, { method: 'POST', body: JSON.stringify({ username: user.username }) });
      onNotice(action === 'disable' ? t('Usuario deshabilitado y sesiones revocadas.') : action === 'enable' ? t('Usuario habilitado.') : t('Sesiones revocadas.'));
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : t('No se pudo cambiar el acceso'));
    } finally {
      setBusyUser('');
    }
  };
  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      onNotice(t('Contraseña temporal copiada'));
    } catch {
      onError(t('No se pudo copiar. Copiala manualmente.'));
    }
  };
  const toggleConnection = (id: string) => {
    setForm((current) => ({
      ...current,
      connections: current.connections.includes(id) ? current.connections.filter((item) => item !== id) : [...current.connections, id],
    }));
  };

  return <section className="user-admin">
    <header className="section-heading">
      <div>
        <p className="eyebrow">{t('CUENTAS LOCALES')}</p>
        <h2>{t('Acceso a la plataforma')}</h2>
        <span className="database-caption">{t('Usuario y contraseña con sesión revocable. Las concesiones se asignan por conexión.')}</span>
      </div>
      <span className="database-toolbar-count">{loading ? t('Cargando...') : loadError ? t('Sin dato') : `${users.length} ${users.length === 1 ? 'cuenta' : 'cuentas'}`}</span>
    </header>
    {created && <aside className="created-credential" role="status">
      <div><strong>{t('Usuario local creado')}</strong><span>{created.username}</span><small>{t('Guardá esta contraseña temporal: no se vuelve a mostrar.')}</small></div>
      <code>{created.password}</code>
      <button className="button subtle" type="button" onClick={() => void copy(created.password)}><Copy size={14} /> {t('Copiar contraseña')}</button>
      <button className="icon-button" type="button" aria-label={t('Ocultar credencial temporal')} onClick={() => setCreated(null)}><X size={15} /></button>
    </aside>}
    <div className="user-directory-toolbar">
      <button className="button primary" type="button" onClick={openCreate}><Plus size={15} /> {t('Crear usuario local')}</button>
    </div>
    {loading ? <p className="user-access-note">{t('Cargando usuarios...')}</p> : loadError ? <p className="user-directory-error" role="alert">{loadError} <button className="button subtle" type="button" onClick={() => void load()}>{t('Reintentar')}</button></p> : <>
      <div className="user-directory-scroll"><table className="user-directory-table">
        <caption className="sr-only">{t('Cuentas locales de Custodia')}</caption>
        <thead><tr><th scope="col">{t('Usuario')}</th><th scope="col">{t('Nombre')}</th><th scope="col">{t('Rol')}</th><th scope="col">{t('Estado')}</th><th scope="col">{t('Último acceso')}</th><th scope="col">{t('Sesiones')}</th><th scope="col">{t('Conexiones')}</th><th scope="col"><span className="sr-only">{t('Acciones')}</span></th></tr></thead>
        <tbody>{users.map((user) => <tr className="person-row" key={user.username}>
          <td><div className="user-row-identity"><strong>{user.username}</strong><span>{user.must_change_password ? t('Contraseña temporal pendiente') : t('Cuenta local')}</span></div></td>
          <td>{user.display_name}</td>
          <td>{user.administrator ? t('Administrador') : t('Lector')}</td>
          <td><span className={`user-status-chip ${!user.enabled ? 'inactive' : user.must_change_password ? 'pending' : 'active'}`}>{!user.enabled ? t('Deshabilitado') : user.must_change_password ? t('Cambio pendiente') : t('Activo')}</span></td>
          <td><DateTime value={user.last_login_at} /></td>
          <td>{user.active_sessions}</td>
          <td>{user.connections.length ? user.connections.join(', ') : t('Sin conexiones')}</td>
          <td><div className="user-access-drawer-actions">
            {user.administrator ? <span className="user-self-badge"><ShieldCheck size={13} /> {t('Administrador')}</span> : <button className="button subtle" type="button" disabled={busyUser === user.username} onClick={() => void act(user, user.enabled ? 'disable' : 'enable')}>{user.enabled ? <><UserRoundX size={14} /> {t('Deshabilitar')}</> : t('Habilitar')}</button>}
            {user.active_sessions > 0 && <button className="button subtle" type="button" disabled={busyUser === user.username} onClick={() => void act(user, 'revoke-sessions')}><KeyRound size={14} /> {t('Revocar sesiones')}</button>}
          </div></td>
        </tr>)}</tbody>
      </table></div>
      {!users.length && <p className="user-access-note">{t('No hay cuentas locales todavía.')}</p>}
    </>}
    {createOpen && <div className="user-detail-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) setCreateOpen(false); }}>
      <div className="user-access-drawer" role="dialog" aria-modal="true" aria-labelledby="create-local-user-title">
        <header className="user-access-drawer-header"><div><p className="eyebrow">{t('CUENTA LOCAL')}</p><h2 id="create-local-user-title">{t('Crear usuario local')}</h2></div><button className="icon-button" type="button" aria-label={t('Cerrar creación de usuario')} disabled={busy} onClick={() => setCreateOpen(false)}><X size={16} /></button></header>
        <form className="user-create-form" noValidate onSubmit={create}>
          <label>{t('Usuario')}<input autoComplete="off" placeholder="bruno" value={form.username} onChange={(event) => setForm({ ...form, username: event.target.value })} required /></label>
          <label>{t('Nombre')}<input autoComplete="off" placeholder={t('Nombre y apellido')} value={form.display_name} onChange={(event) => setForm({ ...form, display_name: event.target.value })} required /></label>
          <label>{t('Contraseña temporal')}<input autoComplete="new-password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} required /></label>
          {connections.length > 0 && <div className="user-access-drawer-section"><strong>{t('Conexiones concedidas')}</strong>{connections.map((connection) => <label key={connection.id}><input type="checkbox" checked={form.connections.includes(connection.id)} onChange={() => toggleConnection(connection.id)} /> {connection.id}</label>)}</div>}
          <p className="database-caption">{t('La persona deberá cambiar la contraseña en su primer ingreso.')}</p>
          <button className="button primary" type="submit" disabled={busy}><Plus size={15} /> {busy ? t('Creando...') : t('Crear usuario local')}</button>
        </form>
      </div>
    </div>}
  </section>;
}
