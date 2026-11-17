import { expect, mockAuthenticatedApp, test } from '../support/fixtures';

test('the console shows the installed version, health and requests a signed update', async ({ page }) => {
  await mockAuthenticatedApp(page, { permissions: ['installation:manage', 'audit:read'] });
  await page.goto('/admin/system');
  await expect(page.getByRole('heading', { name: 'Sistema', exact: true })).toBeVisible();
  await expect(page.getByText('Versión actual', { exact: true })).toBeVisible();
  await expect(page.getByText('0.1.0-demo', { exact: true })).toBeVisible();
  await expect(page.getByText('0.2.0-demo', { exact: true })).toBeVisible();
  await expect(page.getByText('Saludable', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: /Actualizar a 0.2.0-demo/ }).click();
  await expect(page.getByText('Actualización solicitada. El servicio root la aplicará y reiniciará Custodia.')).toBeVisible();
});
