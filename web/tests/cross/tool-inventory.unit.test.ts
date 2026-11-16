import assert from 'node:assert/strict';
import test from 'node:test';

import { collectionStateBadge, completenessBadge, metricFormula, metricReading, reasonText, runStatusBadge, unitLabel } from '../../src/cross/lib/tool-inventory.ts';
import type { InventoryMetric } from '../../src/cross/types/tool-inventory.ts';

function metric(overrides: Partial<InventoryMetric> = {}): InventoryMetric {
  return {
    id: 'cyberark-pam.accounts.total.v1',
    connector: 'cyberark-pam-safes-v1',
    name: 'Cuentas totales',
    description: 'Cuentas activas.',
    collection: 'accounts',
    kind: 'count',
    unit: 'accounts',
    status: 'available',
    value: 12,
    partial: false,
    sources: [{ collection: 'accounts' }],
    ...overrides,
  };
}

test('a missing value is never rendered as zero', () => {
  const unavailable = metricReading(metric({ status: 'unavailable', value: null, reason: 'no_publication' }));
  assert.equal(unavailable.figure, 'No disponible');
  assert.equal(unavailable.drillable, false);
  assert.doesNotMatch(unavailable.figure, /0/);
  assert.match(unavailable.note, /corrida completa o parcial/);

  const withheld = metricReading(metric({ status: 'partial', value: null, partial: true, reason: 'partial_denominator' }));
  assert.equal(withheld.figure, 'Sin calcular');
  assert.equal(withheld.tone, 'warning');
  assert.equal(withheld.drillable, false);
  assert.match(withheld.note, /proporción no se calcula/);
});

test('a confirmed zero is a real drillable value', () => {
  const reading = metricReading(metric({ value: 0 }));
  assert.equal(reading.figure, '0');
  assert.equal(reading.drillable, true);
  assert.equal(reading.tone, 'success');
  assert.equal(reading.state, 'Completa');
});

test('a partial count keeps its value and says so', () => {
  const reading = metricReading(metric({ status: 'partial', value: 3, partial: true }));
  assert.equal(reading.figure, '3');
  assert.equal(reading.tone, 'warning');
  assert.equal(reading.state, 'Parcial');
  assert.match(reading.note, /publicación parcial/);
});

test('a proportion shows its terms and one decimal', () => {
  const reading = metricReading(metric({ kind: 'ratio', unit: 'percent', value: 66.7, numerator: 2, denominator: 3 }));
  assert.equal(reading.figure, '66,7');
  assert.equal(reading.unit, '%');
  assert.match(reading.note, /2 de 3/);
});

test('units follow the metric cardinality', () => {
  assert.equal(unitLabel('accounts', 1), 'cuenta');
  assert.equal(unitLabel('accounts', 2), 'cuentas');
  assert.equal(unitLabel('percent', 1), '%');
  assert.equal(unitLabel('safes', 1), 'Safe');
  assert.equal(unitLabel('safes', 0), 'Safes');
});

test('the explanation spells out the declared calculation and its fields', () => {
  assert.match(metricFormula(metric()), /Conteo/);
  const filtered = metricFormula(metric({ kind: 'count_where', where: { field: 'management_status', values: ['failure'] } }));
  assert.match(filtered, /management_status/);
  assert.match(filtered, /failure/);
  const ratio = metricFormula(metric({
    kind: 'ratio', unit: 'percent',
    where: { field: 'automatic_management_enabled', values: ['false'] },
    denominator_where: { field: 'automatic_management_enabled', values: ['true', 'false'] },
  }));
  assert.match(ratio, /porcentaje/);
  const without = metricFormula(metric({ kind: 'count_without', without: { collection: 'safe_members', field: 'safe_number' } }));
  assert.match(without, /safe_members/);
  assert.match(without, /safe_number/);
  const age = metricFormula(metric({ kind: 'age_over', age_field: 'secret_modified_at', threshold_days: 90 }));
  assert.match(age, /secret_modified_at/);
  assert.match(age, /90/);
});

test('collection, completeness and run badges map to semantic tones', () => {
  assert.deepEqual(completenessBadge('complete'), { label: 'Completa', tone: 'success' });
  assert.equal(completenessBadge('partial').tone, 'warning');
  assert.equal(completenessBadge('unpublished').tone, 'neutral');
  assert.equal(runStatusBadge('failed').tone, 'danger');
  assert.deepEqual(runStatusBadge(undefined), { label: 'Sin corridas', tone: 'neutral' });
  assert.deepEqual(collectionStateBadge({ collection: 'accounts', connection_id: 'pam', state: 'never_published' }), { label: 'Sin publicar', tone: 'neutral' });
  assert.deepEqual(collectionStateBadge({ collection: 'accounts', connection_id: 'pam', state: 'partial' }), { label: 'Parcial', tone: 'warning' });
});

test('unknown reasons fall back to a generic explanation', () => {
  assert.equal(reasonText('future_reason'), 'Sin dato disponible.');
});
