import { UserAdmin } from './user-admin';
import { LocalUsersAdmin } from './local-users-admin';
import { OIDCSettings } from './oidc-settings';
import { AccessReview } from './access-review';
import { RoleAdmin } from './role-admin';
import { useEffect, useState } from 'react';
import { ClipboardCheck, Fingerprint, ShieldCheck, UsersRound } from 'lucide-react';
import { handleTablistKeyDown } from '../lib/tablist-keyboard';
import './user-admin.css';
import { t } from '../i18n/index.ts';

type UsersWorkspaceProps = {
  authMode?: 'local' | 'oidc' | 'hybrid' | 'ozal';
  currentActorId?: string;
  currentUserEmail?: string | null;
  permissions: string[];
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

type AccessView = 'users' | 'oidc' | 'review' | 'roles';

export function UsersWorkspace(props: UsersWorkspaceProps) {
  const has = (permission: string) => props.permissions.includes('access:manage') || props.permissions.includes(permission);
  // Go local mode announces only its own administration scope; the legacy
  // directory/role APIs it does not implement stay hidden there. The static
  // demo and other modes keep the existing workspace.
  // The minimal Go screens announce their own capability and nothing else.
  // Delegated legacy roles (for example access:roles_manage without
  // access:manage) must keep the full directory/roles workspace.
  const localAccountsMode = props.authMode === 'local' && has('access:local_users_manage')
    && !has('access:manage') && !has('access:directory_read') && !has('access:role_assignments_manage');
  const oidcAccountsMode = props.authMode === 'oidc' && has('access:oidc_manage')
    && !has('access:manage') && !has('access:directory_read') && !has('access:role_assignments_manage');
  const goAccessMode = localAccountsMode || oidcAccountsMode;
  const canSeeUsers = has('access:directory_read') || has('access:local_users_manage') || has('access:role_assignments_manage');
  const canSeeOidc = oidcAccountsMode;
  const canSeeReview = !goAccessMode && canSeeUsers;
  const canSeeRoles = !goAccessMode && (has('access:roles_read') || has('access:roles_manage') || has('access:permissions_manage') || has('access:oidc_mappings_read'));
  const [view, setView] = useState<AccessView>(() => canSeeOidc ? 'oidc' : canSeeUsers ? 'users' : 'roles');
  useEffect(() => {
    if (view === 'oidc' && !canSeeOidc) setView(canSeeUsers ? 'users' : 'roles');
    if ((view === 'users' || view === 'review') && !canSeeUsers && canSeeRoles) setView('roles');
    if (view === 'roles' && !canSeeRoles && (canSeeUsers || canSeeOidc)) setView(canSeeOidc ? 'oidc' : 'users');
  }, [canSeeOidc, canSeeRoles, canSeeUsers, view]);
  return <div className="access-workspace">
    <nav className="access-tabs" aria-label={t('Administración de acceso')} role="tablist" onKeyDown={handleTablistKeyDown}>
      {canSeeOidc && <button id="access-tab-oidc" aria-controls="access-tabpanel" className={view === 'oidc' ? 'active' : ''} type="button" role="tab" aria-selected={view === 'oidc'} tabIndex={view === 'oidc' ? 0 : -1} onClick={() => setView('oidc')}><Fingerprint size={16} /><span><strong>{t('Acceso OIDC')}</strong><small>{t('Proveedor, aprobaciones y cuentas')}</small></span></button>}
      {canSeeUsers && <button id="access-tab-users" aria-controls="access-tabpanel" className={view === 'users' ? 'active' : ''} type="button" role="tab" aria-selected={view === 'users'} tabIndex={view === 'users' ? 0 : -1} onClick={() => setView('users')}><UsersRound size={16} /><span><strong>{t('Usuarios de Custodia')}</strong><small>{t('Identidades locales y Okta')}</small></span></button>}
      {canSeeReview && <button id="access-tab-review" aria-controls="access-tabpanel" className={view === 'review' ? 'active' : ''} type="button" role="tab" aria-selected={view === 'review'} tabIndex={view === 'review' ? 0 : -1} onClick={() => setView('review')}><ClipboardCheck size={16} /><span><strong>{t('Revisión de accesos')}</strong><small>{t('Quién tiene qué y desde cuándo')}</small></span></button>}
      {canSeeRoles && <button id="access-tab-roles" aria-controls="access-tabpanel" className={view === 'roles' ? 'active' : ''} type="button" role="tab" aria-selected={view === 'roles'} tabIndex={view === 'roles' ? 0 : -1} onClick={() => setView('roles')}><ShieldCheck size={16} /><span><strong>{t('Roles y permisos')}</strong><small>{t('Alcance efectivo por producto')}</small></span></button>}
    </nav>
    <div role="tabpanel" id="access-tabpanel" aria-labelledby={`access-tab-${view}`}>
      {view === 'oidc' && canSeeOidc ? <OIDCSettings onNotice={props.onNotice} onError={props.onError} /> : view === 'users' && canSeeUsers ? (localAccountsMode ? <LocalUsersAdmin onNotice={props.onNotice} onError={props.onError} /> : <UserAdmin {...props} />) : view === 'review' && canSeeReview ? <AccessReview /> : view === 'roles' && canSeeRoles ? <RoleAdmin permissions={props.permissions} currentActorId={props.currentActorId} onNotice={props.onNotice} onError={props.onError} /> : null}
    </div>
  </div>;
}
