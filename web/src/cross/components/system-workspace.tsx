import { useCallback, useEffect, useState } from 'react';
import { Activity, RefreshCw, ServerCog } from 'lucide-react';
import { api } from '../lib/api';
import { StateBlock } from './state-block';
import { t } from '../i18n/index.ts';
import './system-workspace.css';
import './user-admin.css';
import './connection-onboarding.css';
import './oidc-settings.css';

type VersionInfo = { current: string; available: string; update_available: boolean; manifest_configured: boolean; mode: string };
type Health = { status: string; version: string; mode: string };
type Backup = { name: string; size: number; created_at: string };
type Diagnostic = { group_id: string; connector: string; auth_mode: string; secret_configured: boolean; interval_minutes: number; enabled: boolean; retired_at: string; last_probe: { ok: boolean; at: string } | null };

export function SystemWorkspace({ onNotice, onError }: { onNotice: (message: string) => void; onError: (message: string) => void }) {
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const [health, setHealth] = useState<Health | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [backups, setBackups] = useState<Backup[]>([]);
  const [diagnostics, setDiagnostics] = useState<Diagnostic[]>([]);
  const [backupBusy, setBackupBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [version, healthResponse, backupRows, diagnosticRows] = await Promise.all([
        api<VersionInfo>('/system/version'),
        fetch('/api/health', { headers: { Accept: 'application/json' } }).then((response) => response.ok ? response.json() as Promise<Health> : null).catch(() => null),
        api<{ rows: Backup[] }>('/system/backups').then((data) => data.rows).catch(() => []),
        api<{ rows: Diagnostic[] }>('/system/diagnostics').then((data) => data.rows).catch(() => []),
      ]);
      setInfo(version);
      setHealth(healthResponse);
      setBackups(backupRows);
      setDiagnostics(diagnosticRows);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('No se pudo consultar la versión'));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const createBackup = async () => {
    setBackupBusy(true);
    try {
      await api('/system/backups', { method: 'POST', body: '{}' });
      onNotice(t('Backup creado. La clave maestra no se incluye; conservala por separado.'));
      await load();
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('No se pudo crear el backup'));
    } finally {
      setBackupBusy(false);
    }
  };

  const requestUpdate = async () => {
    setBusy(true);
    try {
      await api('/system/update', { method: 'POST', body: '{}' });
      onNotice(t('Actualización solicitada. El servicio root la aplicará y reiniciará Custodia.'));
      await load();
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('No se pudo solicitar la actualización'));
    } finally {
      setBusy(false);
    }
  };

  return <section className="system-workspace">
    <header className="section-heading">
      <div>
        <p className="eyebrow">{t('INSTALACIÓN')}</p>
        <h2>{t('Versión y actualizaciones')}</h2>
        <span className="database-caption">{t('Versión, salud y actualización firmada de esta instancia.')}</span>
      </div>
      <span className={`oidc-status ${health?.status === 'ok' ? 'active' : 'inactive'}`}>{health?.status === 'ok' ? t('Saludable') : t('Sin salud')}</span>
    </header>
    {loading ? <p className="user-access-note">{t('Consultando la instancia...')}</p> : error ? <StateBlock variant="error" title={t('No se pudo consultar la instancia')} hint={error} /> : info ? <>
      <div className="system-grid">
        <article><span>{t('Versión actual')}</span><strong>{info.current}</strong></article>
        <article><span>{t('Versión disponible')}</span><strong>{info.available || t('Sin manifiesto')}</strong></article>
        <article><span>{t('Modo de acceso')}</span><strong>{info.mode}</strong></article>
        <article><span>{t('Salud')}</span><strong>{health ? `${health.status} · ${health.version}` : t('Sin respuesta')}</strong></article>
      </div>
      <div className="system-actions">
        <button className="button primary" type="button" disabled={!info.update_available || busy} onClick={() => void requestUpdate()}><RefreshCw size={15} /> {busy ? t('Solicitando...') : t('Actualizar a {version}', { version: info.available || '—' })}</button>
        <button className="button subtle" type="button" onClick={() => void load()}><Activity size={15} /> {t('Volver a comprobar')}</button>
      </div>
      <p className="database-caption"><ServerCog size={14} /> {t('La solicitud no cambia el binario desde el proceso de Custodia: la aplica un servicio root con firma, backup, migración, salud y rollback.')}</p>

      <section className="system-section"><header><div><h3>{t('Backups')}</h3><p className="database-caption">{t('Copia consistente de SQLite. No incluye la clave maestra; guardala por separado. La agenda diaria la ejecuta systemd.')}</p></div><button className="button subtle" type="button" disabled={backupBusy} onClick={() => void createBackup()}>{backupBusy ? t('Creando...') : t('Crear backup')}</button></header>
        {backups.length === 0 ? <p className="user-access-note">{t('No hay backups todavía.')}</p> : <table className="connection-probe-table"><caption className="sr-only">{t('Backups disponibles')}</caption><thead><tr><th scope="col">{t('Archivo')}</th><th scope="col">{t('Creado')}</th><th scope="col">{t('Tamaño')}</th><th scope="col"><span className="sr-only">{t('Acciones')}</span></th></tr></thead><tbody>{backups.map((backup) => <tr key={backup.name}><td>{backup.name}</td><td>{backup.created_at}</td><td>{backup.size} B</td><td><a className="button subtle" href={`/api/system/backups/${encodeURIComponent(backup.name)}`}>{t('Descargar')}</a></td></tr>)}</tbody></table>}
      </section>

      <section className="system-section"><header><div><h3>{t('Diagnóstico de conectores')}</h3><p className="database-caption">{t('Estado, secreto, probe y agenda por conexión visible.')}</p></div></header>
        {diagnostics.length === 0 ? <p className="user-access-note">{t('No hay conexiones registradas.')}</p> : <table className="connection-probe-table"><caption className="sr-only">{t('Diagnóstico de conectores')}</caption><thead><tr><th scope="col">{t('Conexión')}</th><th scope="col">{t('Secreto')}</th><th scope="col">{t('Probe')}</th><th scope="col">{t('Agenda')}</th><th scope="col">{t('Estado')}</th></tr></thead><tbody>{diagnostics.map((row) => <tr key={row.group_id}><td>{row.group_id}<br /><small>{row.connector}</small></td><td>{row.secret_configured ? t('Cifrado') : t('Referencia')}</td><td>{row.last_probe ? (row.last_probe.ok ? t('Correcta') : t('Con errores')) : t('Sin probar')}</td><td>{row.interval_minutes > 0 ? `${row.interval_minutes} min` : t('Apagada')}</td><td>{row.retired_at ? t('Retirada') : row.enabled ? t('Activa') : t('Pausada')}</td></tr>)}</tbody></table>}
      </section>
    </> : null}
  </section>;
}
