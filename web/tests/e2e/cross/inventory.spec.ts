import { expect, mockAuthenticatedApp, test } from '../support/fixtures';

test('inventory renders declared metrics and drills into the exact records', async ({ page }) => {
  await mockAuthenticatedApp(page);
  await page.goto('/inventory');
  await expect(page.getByText('Inventario de herramientas', { exact: true })).toBeVisible();
  const card = page.locator('.tool-inventory-card');
  await expect(card.getByRole('heading', { name: 'CyberArk PAM', exact: true })).toBeVisible();
  await expect(page.getByText('1 conexiones visibles')).toBeVisible();
  await expect(card).toContainText('Safes totales');
  await expect(card).toContainText('Cuentas totales');
  // A metric without its collection is unavailable, never zero.
  const unavailable = card.locator('[data-metric="cyberark-pam.safes.without_members.v1"]');
  await expect(unavailable).toContainText('No disponible');
  await expect(unavailable.locator('.tool-metric-figure')).not.toContainText('0');
  // A confirmed zero is a real value.
  const failed = card.locator('[data-metric="cyberark-pam.accounts.last_change_failed.v1"]');
  await expect(failed.locator('.tool-metric-figure')).toContainText('0');
  // Drill-down shows exactly the records that compose the value.
  await card.getByRole('button', { name: 'Ver registros de Safes totales' }).click();
  const records = page.getByRole('region', { name: 'Registros de Safes totales' });
  await expect(records).toContainText('2 registros');
  await expect(records).toContainText('ACME-PROD-PLATFORM');
  await expect(records).toContainText('ACME-DEV-SERVICES');
  // The account drill-down filters by the same synthetic records.
  await card.getByRole('button', { name: 'Ver registros de Cuentas con gestión automática deshabilitada' }).click();
  const accounts = page.getByRole('region', { name: 'Registros de Cuentas con gestión automática deshabilitada' });
  await expect(accounts).toContainText('1 registros');
  await expect(accounts).toContainText('dev-services-admin');
});
