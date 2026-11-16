import type { ConnectorDescriptor } from '../types/ingestion';

/** Operator-entered fields for connecting one tenant. Credentials are always
 *  references (`env:`, `file:`, `secret:`), never values. */
export type ConnectionForm = {
  connector: string;
  baseUrl: string;
  collection: string;
  scope: string;
  authMode: string;
  credentialRef: string;
  tokenUrl: string;
  clientIdRef: string;
  clientSecretRef: string;
  scopes: string;
  caFile: string;
  clientCertFile: string;
  clientKeyFile: string;
  interval: string;
};

export type ConnectionRequest = {
  connector: string;
  base_url: string;
  collection: string;
  scope: string;
  auth_mode: string;
  credential_ref?: string;
  token_url?: string;
  client_id_ref?: string;
  client_secret_ref?: string;
  scopes?: string[];
  ca_file?: string;
  client_cert_file?: string;
  client_key_file?: string;
  interval_minutes: number;
};

export type ConnectionDraft = { ok: true; request: ConnectionRequest } | { ok: false; errors: string[] };

export function emptyConnectionForm(descriptor?: ConnectorDescriptor): ConnectionForm {
  return {
    connector: descriptor?.id ?? '',
    baseUrl: '',
    collection: descriptor?.collections[0] ?? '',
    scope: '',
    authMode: descriptor?.auth_modes[0] ?? '',
    credentialRef: '',
    tokenUrl: '',
    clientIdRef: '',
    clientSecretRef: '',
    scopes: '',
    caFile: '',
    clientCertFile: '',
    clientKeyFile: '',
    interval: '0',
  };
}

/** Only implemented connectors can be connected; planned tools stay visible as
 *  roadmap but never offer endpoints or credentials. */
export function connectableConnectors(rows: ConnectorDescriptor[]): ConnectorDescriptor[] {
  return rows.filter((row) => row.status === 'available');
}

export function isReference(value: string): boolean {
  return /^(env|file|secret):.+/.test(value.trim());
}

export function isAbsolutePath(value: string): boolean {
  return value.trim().startsWith('/') && !/[\r\n]/.test(value);
}

export function splitScopes(value: string): string[] {
  return value.split(/[\s,]+/).map((scope) => scope.trim()).filter(Boolean);
}

export function buildConnectionRequest(form: ConnectionForm, descriptor: ConnectorDescriptor): ConnectionDraft {
  const errors: string[] = [];
  if (descriptor.status !== 'available') {
    errors.push('La herramienta todavía no tiene un contrato implementado.');
  }
  let base: URL | null = null;
  try {
    base = new URL(form.baseUrl.trim());
  } catch {
    errors.push('La URL del tenant no es válida.');
  }
  if (base) {
    if (base.protocol !== 'https:') errors.push('La URL del tenant debe usar HTTPS.');
    if (base.username || base.password) errors.push('La URL no debe incluir credenciales.');
    if (base.search || base.hash) errors.push('La URL no debe incluir query ni fragmento.');
  }
  if (!descriptor.collections.includes(form.collection)) {
    errors.push('Elegí una colección implementada para esta herramienta.');
  }
  if (!form.scope.trim()) errors.push('El alcance es obligatorio.');
  if (!descriptor.auth_modes.includes(form.authMode)) {
    errors.push('Elegí un modo de autenticación soportado.');
  }
  if (form.authMode === 'bearer' && !isReference(form.credentialRef)) {
    errors.push('La credencial debe ser una referencia env:, file: o secret:.');
  }
  if (form.authMode === 'oauth-client-credentials') {
    try {
      const token = new URL(form.tokenUrl.trim());
      if (token.protocol !== 'https:') errors.push('El endpoint de token debe usar HTTPS.');
    } catch {
      errors.push('El endpoint de token no es válido.');
    }
    if (!isReference(form.clientIdRef)) errors.push('El client ID debe ser una referencia.');
    if (!isReference(form.clientSecretRef)) errors.push('El client secret debe ser una referencia.');
  }
  if (form.authMode === 'mtls') {
    if (!isAbsolutePath(form.clientCertFile) || !isAbsolutePath(form.clientKeyFile)) {
      errors.push('mTLS requiere rutas absolutas de certificado y clave.');
    }
  }
  if (form.caFile.trim() && !isAbsolutePath(form.caFile)) {
    errors.push('La CA debe ser una ruta absoluta local.');
  }
  const interval = form.interval.trim() === '' ? 0 : Number(form.interval);
  if (!Number.isInteger(interval) || interval < 0 || interval > 525600 || (interval > 0 && interval < 5)) {
    errors.push('Usá 0 para deshabilitar o un intervalo de al menos 5 minutos.');
  }
  if (errors.length) return { ok: false, errors };

  const request: ConnectionRequest = {
    connector: descriptor.id,
    base_url: form.baseUrl.trim(),
    collection: form.collection,
    scope: form.scope.trim(),
    auth_mode: form.authMode,
    interval_minutes: interval,
  };
  if (form.authMode === 'bearer') request.credential_ref = form.credentialRef.trim();
  if (form.authMode === 'oauth-client-credentials') {
    request.token_url = form.tokenUrl.trim();
    request.client_id_ref = form.clientIdRef.trim();
    request.client_secret_ref = form.clientSecretRef.trim();
    const scopes = splitScopes(form.scopes);
    if (scopes.length) request.scopes = scopes;
  }
  if (form.authMode === 'mtls') {
    request.client_cert_file = form.clientCertFile.trim();
    request.client_key_file = form.clientKeyFile.trim();
  }
  if (form.caFile.trim()) request.ca_file = form.caFile.trim();
  return { ok: true, request };
}
