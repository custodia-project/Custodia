import { expect, mockAuthenticatedApp, test } from '../support/fixtures';

test('Wazuh connects with a read-only API user and an encrypted password', async ({ page }) => {
  await mockAuthenticatedApp(page, { permissions: ['inventory:read', 'access:manage', 'ingestion:run', 'installation:manage', 'audit:read'] });
  await page.goto('/admin/activity');
  await page.getByRole('tab', { name: 'Conexiones', exact: true }).click();
  await page.getByLabel('Herramienta', { exact: true }).selectOption('wazuh');
  await expect(page.getByLabel('Autenticación', { exact: true })).toHaveValue('basic-exchange');
  await page.getByLabel('URL del tenant', { exact: true }).fill('https://wazuh.acme.example:55000');
  await page.getByLabel('Alcance', { exact: true }).fill('acme-wazuh');
  await page.getByLabel('Usuario API', { exact: true }).fill('custodia-reader');
  await page.getByLabel('Secreto', { exact: true }).fill('a read-only wazuh password');
  await page.getByRole('button', { name: 'Conectar tenant' }).click();
  const row = page.getByRole('row', { name: /acme-wazuh/ });
  await expect(row).toBeVisible();
  await expect(row).toContainText('basic-exchange');
  await expect(row).toContainText('Cifrado v1');
  await row.getByRole('button', { name: 'Probar', exact: true }).click();
  await expect(row).toContainText('Correcta');
});
