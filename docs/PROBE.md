---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Probe PAM autorizado

**Estado:** transporte preparado, probado contra fixtures TLS locales y
cableado al comando `probe-pam`. La validación del plan, el transporte de
lectura, OAuth client-credentials, mTLS y el proveedor `secret:` están
implementados en `server/internal/probe`; la aceptación contra un tenant
**no** se ejecutó. Nada de este documento autoriza un probe real.

## Condiciones previas

Un probe sólo puede ejecutarse cuando el dueño del tenant autoriza por escrito:

- la URL HTTPS exacta y el host;
- la identidad de servicio y el alcance mínimo de lectura;
- la referencia de credencial (`env:`, `file:` o `secret:`), nunca el valor;
- la CA adicional, si el tenant usa una CA privada;
- los endpoints permitidos y el presupuesto de consulta;
- quién recibe la evidencia y cuánto se conserva.

El probe es de sólo lectura, no escribe SQLite, no publica observaciones, no
descarga secretos de cuentas y no sigue redirecciones ni hosts distintos al
configurado.

## Plan validado

`probe.NewPlan` acepta únicamente:

| Campo | Regla |
|---|---|
| `BaseURL` | `https://`, absoluta, sin usuario, query, fragmento ni path. |
| `Authorized` | Debe ser `true`; representa autorización explícita del dueño. |
| `AuthMode` | `bearer`, `oauth-client-credentials` o `mtls`. |
| `CredentialRef` | Bearer: referencia `env:`, `file:` o `secret:`; nunca un valor. |
| `TokenURL` | OAuth: endpoint HTTPS del token, sin query ni fragmento. |
| `ClientIDRef` / `ClientSecretRef` | OAuth: referencias `env:`, `file:` o `secret:`; nunca valores. |
| `Scopes` | OAuth: scopes opcionales, acotados y sin espacios internos. |
| `ClientCertFile` / `ClientKeyFile` | mTLS: paths locales absolutos del certificado y clave. |
| `CAFile` | Path local absoluto opcional, sin esquema ni caracteres de control. |
| `Endpoints` | Sólo `/API/Accounts`, `/API/Platforms`, `/API/PSM/Connectors`, `/API/PSM/Servers` y `/API/Safes`. |
| `Timeout` | Entre 1 s y 2 min; por defecto 15 s. |

El plan resultante es saneado y puede registrarse sin exponer credenciales.

## Transporte implementado

- `ResolveCredential` resuelve `env:`, `file:` absoluto y `secret:NAME` desde
  `CUSTODIA_PROBE_SECRET_DIR`; el directorio debe ser absoluto, el archivo
  regular, no symlink y acotado. `secret:` no usa el repositorio.
- TLS verificado con mínimo TLS 1.2; CA adicional por archivo, sin
  `InsecureSkipVerify` ni proxy implícito.
- OAuth client-credentials: POST `grant_type=client_credentials` al
  `TokenURL`, `client_id`/`client_secret` resueltos por referencia y scopes
  opcionales; sólo se acepta `token_type=Bearer`.
- mTLS: certificado y clave locales absolutos; el servidor exige el certificado
  de cliente.
- Timeout total por endpoint, cuerpo máximo de 1 MiB y GET secuenciales.
- Redirecciones no seguidas: un `3xx` se reporta como
  `redirect_not_followed`; el endpoint de token tampoco sigue redirects.
- Reporte saneado por endpoint: estado, duración, cantidad de objetos y código
  estable (`authentication_failed`, `permission_denied`, `rate_limited`,
  `timeout`, `transport_error`, `invalid_content_type`, `invalid_json`,
  `invalid_payload`, `body_too_large`). Nunca incluye cuerpos, headers, tokens
  ni valores de secreto.

Las pruebas usan `httptest.NewTLSServer` y un servidor mTLS con CA temporal;
no contactan ningún tenant.

## Comando

El comando ya está cableado y emite el reporte saneado como JSON:

```sh
# Bearer
custodia probe-pam --authorized --auth bearer \
  --url https://tenant.example \
  --credential-ref secret:bearer-token \
  --ca-file /ruta/acme-ca.pem \
  --endpoint /API/Platforms --endpoint /API/PSM/Servers

# OAuth client-credentials
custodia probe-pam --authorized --auth oauth-client-credentials \
  --url https://tenant.example \
  --token-url https://tenant.example/oauth2/platformtoken \
  --client-id-ref env:CUSTODIA_PAM_CLIENT_ID \
  --client-secret-ref secret:pam-client-secret \
  --oauth-scope pam.read \
  --ca-file /ruta/acme-ca.pem \
  --endpoint /API/Safes

# mTLS
custodia probe-pam --authorized --auth mtls \
  --url https://tenant.example \
  --client-cert-file /ruta/cliente.pem \
  --client-key-file /ruta/cliente.key \
  --ca-file /ruta/acme-ca.pem \
  --endpoint /API/Platforms
```

`secret:NAME` lee `CUSTODIA_PROBE_SECRET_DIR/NAME`; el directorio debe estar
fuera del repositorio y contener archivos regulares acotados. Los valores nunca
se imprimen.

Códigos de salida:

| Código | Significado |
|---|---|
| `0` | Todos los endpoints respondieron `200` sin error de contrato. |
| `1` | El reporte se emitió, pero al menos un endpoint falló o devolvió un estado distinto de `200`. |
| `2` | El plan, la referencia de credencial, la autenticación o la CA son inválidos; no se emitió reporte. |

## Pendiente

La siguiente slice debe agregar, sólo tras la autorización:

1. Validar el contrato real de OAuth/mTLS del tenant y los scopes efectivos.
2. Resolver `secret:` contra el proveedor de secretos de producción.
3. Evidencia de red real: proxy, CA, DNS, rate limit y permisos efectivos.
4. Registro de aceptación con fecha, endpoints y salida saneada.

Contrato propuesto, todavía no cableado al CLI:

```sh
custodia probe-pam --authorized \
  --url https://tenant.example \
  --credential-ref env:CUSTODIA_PAM_CLIENT_SECRET \
  --ca-file /ruta/acme-ca.pem \
  --endpoint /API/Platforms --endpoint /API/PSM/Servers
```

## Criterio de aceptación

- El plan rechaza HTTP, hosts con credenciales embebidas, query, fragmento,
  path, endpoints no allowlisted y referencias de credencial inválidas.
- El probe real no se ejecuta sin autorización explícita y credencial resuelta
  localmente.
- La salida no contiene secretos ni payloads completos.
- Un `401` se distingue de un `403`; un `429` no se convierte en fallo de
  contrato.
- No se persisten observaciones y no se tocan tablas de SQLite.
- La aceptación contra un tenant queda registrada en
  [IMPLEMENTED.md](IMPLEMENTED.md) con fecha, endpoints y evidencia saneada.
