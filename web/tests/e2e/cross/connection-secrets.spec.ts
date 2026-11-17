import { expect, mockAuthenticatedApp, test } from '../support/fixtures';

test('a secret is loaded once, encrypted, probed, scheduled, rotated, paused and retired from the console', async ({ page }) => {
  await mockAuthenticatedApp(page, { permissions: ['inventory:read', 'access:manage', 'ingestion:run', 'installation:manage', 'audit:read'] });
  await page.goto('/admin/activity');
  await page.getByRole('tab', { name: 'Conexiones', exact: true }).click();
  await page.getByLabel('Herramienta', { exact: true }).selectOption('cyberark-pam-safes-v1');
  await page.getByLabel('URL del tenant', { exact: true }).fill('https://pvwa.acme.example/PasswordVault');
  await page.getByLabel('Alcance', { exact: true }).fill('pam-secrets');
  await page.getByLabel('Secreto', { exact: true }).fill('a write-only tenant secret');
  await page.getByRole('button', { name: 'Conectar tenant' }).click();

  const row = page.getByRole('row', { name: /acme-cyberark-pam-safes/ });
  await expect(row).toBeVisible();
  await expect(row).toContainText('Cifrado v1');
  await expect(row).toContainText('Sin probar');

  await row.getByRole('button', { name: 'Probar', exact: true }).click();
  await expect(row).toContainText('Correcta');

  await row.getByRole('button', { name: 'Editar', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Editar conexión' });
  await edit.getByLabel('Intervalo', { exact: true }).fill('15');
  await edit.getByRole('button', { name: 'Guardar cambios' }).click();
  await expect(row).toContainText('Cada 15 min');

  await row.getByRole('button', { name: 'Rotar secreto' }).click();
  const rotate = page.getByRole('dialog', { name: 'Rotar secreto' });
  await rotate.getByLabel('Nuevo secreto', { exact: true }).fill('the rotated tenant secret');
  await rotate.getByRole('button', { name: 'Rotar secreto' }).click();
  await expect(row).toContainText('Cifrado v2');
  await expect(row).toContainText('Sin probar');

  await row.getByRole('button', { name: 'Pausar', exact: true }).click();
  await expect(row).toContainText('Pausada');
  await row.getByRole('button', { name: 'Reanudar', exact: true }).click();
  await expect(row).toContainText('Activa');
  await row.getByRole('button', { name: 'Retirar', exact: true }).click();
  const retire = page.getByRole('alertdialog', { name: 'Retirar conexión' });
  await retire.getByRole('button', { name: 'Retirar conexión' }).click();
  await expect(row).toContainText('Retirada');
});
