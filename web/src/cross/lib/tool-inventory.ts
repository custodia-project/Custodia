/**
 * Presentación del inventario de herramientas: textos y tonos de cada métrica
 * declarada por conector. Nunca convierte un dato ausente en cero: sólo un
 * valor publicado (incluido el cero confirmado) se muestra como número.
 */
import type { InventoryCollection, InventoryMetric, InventoryTool } from '../types/tool-inventory';
import { formatNumber } from './format.ts';
import { localeTag, t } from '../i18n/index.ts';

export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

const UNIT_LABELS: Record<string, [string, string]> = {
  safes: ['Safe', 'Safes'],
  accounts: ['cuenta', 'cuentas'],
  platforms: ['plataforma', 'plataformas'],
};

export function unitLabel(unit: string, value: number): string {
  const labels = UNIT_LABELS[unit];
  if (!labels) return unit === 'percent' ? '%' : unit;
  return t(value === 1 ? labels[0] : labels[1]);
}

const REASONS: Record<string, string> = {
  collection_unavailable: 'La colección no está conectada en esta conexión o no tenés permiso de lectura.',
  no_publication: 'Todavía no hay una corrida completa o parcial que haya publicado datos.',
  partial_denominator: 'La última publicación es parcial: una proporción no se calcula con un denominador parcial.',
  partial_reference: 'La colección relacionada es parcial: faltarían registros para afirmar este valor.',
  empty_denominator: 'No hay registros que informen el dato del denominador; la proporción no tiene valor.',
};

export function reasonText(reason: string | undefined): string {
  return reason && REASONS[reason] ? t(REASONS[reason]) : t('Sin dato disponible.');
}

export type MetricReading = {
  /** Main figure, or a word when there is no value. Never "0" for missing data. */
  figure: string;
  unit: string;
  state: string;
  tone: Tone;
  note: string;
  drillable: boolean;
};

function formatPercent(value: number): string {
  return new Intl.NumberFormat(localeTag(), { maximumFractionDigits: 1, minimumFractionDigits: 0 }).format(value);
}

export function metricReading(metric: InventoryMetric): MetricReading {
  if (metric.value === null || metric.value === undefined) {
    const withheld = metric.status === 'partial';
    return {
      figure: withheld ? t('Sin calcular') : t('No disponible'),
      unit: '',
      state: withheld ? t('Parcial') : t('No disponible'),
      tone: withheld ? 'warning' : 'neutral',
      note: reasonText(metric.reason),
      drillable: false,
    };
  }
  const isPercent = metric.unit === 'percent';
  const figure = isPercent ? formatPercent(metric.value) : formatNumber(metric.value);
  const unit = unitLabel(metric.unit, metric.value);
  let note = '';
  if (metric.kind === 'ratio' && metric.numerator !== undefined && metric.denominator !== undefined) {
    note = t('{numerator} de {denominator}', { numerator: formatNumber(metric.numerator), denominator: formatNumber(metric.denominator) });
  }
  if (metric.partial) {
    note = [note, t('Valor sobre una publicación parcial: puede faltar información.')].filter(Boolean).join(' · ');
  }
  return {
    figure, unit,
    state: metric.partial ? t('Parcial') : t('Completa'),
    tone: metric.partial ? 'warning' : 'success',
    note,
    drillable: true,
  };
}

const COMPLETENESS: Record<InventoryTool['completeness'], { label: string; tone: Tone }> = {
  complete: { label: 'Completa', tone: 'success' },
  partial: { label: 'Parcial', tone: 'warning' },
  incomplete: { label: 'Colecciones sin publicar', tone: 'warning' },
  unpublished: { label: 'Sin datos publicados', tone: 'neutral' },
};

export function completenessBadge(completeness: InventoryTool['completeness']): { label: string; tone: Tone } {
  const entry = COMPLETENESS[completeness] ?? COMPLETENESS.unpublished;
  return { label: t(entry.label), tone: entry.tone };
}

const RUN_STATUS: Record<string, { label: string; tone: Tone }> = {
  completed: { label: 'Completa', tone: 'success' },
  partial: { label: 'Parcial', tone: 'warning' },
  failed: { label: 'Fallida', tone: 'danger' },
  running: { label: 'En curso', tone: 'info' },
  queued: { label: 'En cola', tone: 'info' },
};

export function runStatusBadge(status: string | undefined): { label: string; tone: Tone } {
  if (!status) return { label: t('Sin corridas'), tone: 'neutral' };
  const entry = RUN_STATUS[status];
  return entry ? { label: t(entry.label), tone: entry.tone } : { label: status, tone: 'neutral' };
}

export function collectionStateBadge(collection: InventoryCollection): { label: string; tone: Tone } {
  return collection.state === 'never_published'
    ? { label: t('Sin publicar'), tone: 'neutral' }
    : runStatusBadge(collection.state);
}

/** Human explanation of the declared calculation, including fixed thresholds. */
export function metricFormula(metric: InventoryMetric): string {
  const condition = (where?: { field: string; values: string[] }) => where ? `${where.field} ∈ {${where.values.join(', ')}}` : '';
  switch (metric.kind) {
    case 'count':
      return t('Conteo de registros activos de {collection}.', { collection: metric.collection });
    case 'count_where':
      return t('Conteo de {collection} con {condition}.', { collection: metric.collection, condition: condition(metric.where) });
    case 'ratio':
      return t('{numerator} sobre {denominator}, en porcentaje.', {
        numerator: condition(metric.where),
        denominator: metric.denominator_where ? condition(metric.denominator_where) : t('todos los registros de {collection}', { collection: metric.collection }),
      });
    case 'age_over':
      return t('Registros de {collection} con {field} anterior a {days} días antes de la publicación.', { collection: metric.collection, field: metric.age_field ?? '', days: metric.threshold_days ?? 0 });
    case 'count_without':
      return t('Registros de {collection} sin ningún {field} que los referencie en {reference}.', { collection: metric.collection, field: metric.without?.field ?? '', reference: metric.without?.collection ?? '' });
    default:
      return metric.kind;
  }
}

/** Oldest attribute keys first so the drill-down table has stable columns. */
export function recordColumns(records: { attributes: Record<string, string> }[], max = 5): string[] {
  const seen: string[] = [];
  for (const record of records) {
    for (const key of Object.keys(record.attributes)) {
      if (!seen.includes(key)) seen.push(key);
    }
  }
  return seen.slice(0, max);
}
