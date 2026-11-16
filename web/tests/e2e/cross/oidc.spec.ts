import { expect, mockAuthenticatedApp, test } from '../support/fixtures';

test('OIDC mode signs in through the provider and keeps an explicit administrator form', async ({ page }) => {
  await mockAuthenticatedApp(page, { authMode: 'oidc', oidcEnabled: true, authenticated: false });
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Continuar con el proveedor', exact: true })).toBeVisible();
  await expect(page.getByText('Acceso de administrador local', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Usuario', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Contraseña', { exact: true })).toBeVisible();
  // The provider path is a full navigation, not a silent fallback to the form.
  await page.getByRole('link', { name: 'Continuar con el proveedor', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Abrir command center' })).toBeVisible();
  await expect(page.getByText('Ada Externa', { exact: true }).first()).toBeVisible();
});

test('an administrator configures, tests and activates OIDC and approves a pending identity', async ({ page }) => {
  await mockAuthenticatedApp(page, {
    authMode: 'oidc',
    permissions: ['inventory:read', 'access:local_users_manage', 'access:oidc_manage'],
  });
  await page.goto('/users');
  await page.getByRole('tab', { name: /Acceso OIDC/ }).click();
  await expect(page.getByRole('heading', { name: 'Acceso OIDC', exact: true })).toBeVisible();
  await expect(page.getByText('Sin configurar', { exact: true })).toBeVisible();
  await expect(page.getByText('Ada Externa', { exact: true }).first()).toBeVisible();

  await page.getByLabel('Emisor (issuer)', { exact: true }).fill('https://idp.acme.example');
  await page.getByLabel('Client ID', { exact: true }).fill('custodia-demo');
  await page.getByRole('button', { name: 'Guardar borrador' }).click();
  await expect(page.getByText('Borrador', { exact: true })).toBeVisible();
  // Activation stays disabled until the saved configuration passes the test.
  await expect(page.getByRole('button', { name: 'Activar proveedor' })).toBeDisabled();
  await page.getByRole('button', { name: 'Probar configuración' }).click();
  await expect(page.getByText(/Descubrimiento y claves verificados/)).toBeVisible();
  await page.getByRole('button', { name: 'Activar proveedor' }).click();
  await expect(page.getByText('Activo', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: 'Aprobar', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Aprobar identidad' });
  await expect(dialog).toContainText('sub-ada');
  await dialog.getByRole('checkbox', { name: 'acme-identity' }).check();
  await dialog.getByRole('button', { name: 'Aprobar identidad' }).click();
  await expect(page.getByText('Todavía no hay cuentas OIDC aprobadas.')).toHaveCount(0);
  const accounts = page.getByRole('table', { name: 'Cuentas OIDC aprobadas' });
  await expect(accounts).toContainText('Ada Externa');
  await expect(accounts).toContainText('acme-identity');
});
