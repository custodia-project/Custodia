import { useCallback, useEffect, useState } from 'react';
import { Boxes, ListTree } from 'lucide-react';
import { ApiError } from '../lib/api';
import { fetchInventoryMetricRecords, fetchInventoryTools } from '../lib/tool-inventory-api';
import {
  collectionStateBadge, completenessBadge, metricFormula, metricReading, recordColumns, runStatusBadge,
} from '../lib/tool-inventory';
import type { InventoryMetric, InventoryMetricRecords, InventoryTool } from '../types/tool-inventory';
import { DateTime } from './date-time';
import { StateBlock } from './state-block';
import { StatusBadge } from './semantic';
import { formatNumber } from '../lib/format';
import { t } from '../i18n/index.ts';
import './tool-inventory-workspace.css';

type Selection = { connectionId: string; metric: InventoryMetric };

function MetricCard({ metric, selected, onOpen }: { metric: InventoryMetric; selected: boolean; onOpen: () => void }) {
  const reading = metricReading(metric);
  const figure = <span className="tool-metric-figure">
    <strong>{reading.figure}</strong>{reading.unit ? <span>{reading.unit}</span> : null}
  </span>;
  return <li className={`tool-metric${selected ? ' is-selected' : ''}${reading.drillable ? '' : ' is-empty'}`} data-metric={metric.id}>
    <div className="tool-metric-head">
      <span className="tool-metric-name">{t(metric.name)}</span>
      <StatusBadge tone={reading.tone}>{reading.state}</StatusBadge>
    </div>
    {reading.drillable
      ? <button type="button" className="tool-metric-open" aria-pressed={selected} onClick={onOpen} aria-label={t('Ver registros de {name}', { name: t(metric.name) })}>{figure}</button>
      : figure}
    {reading.note ? <p className="tool-metric-note">{reading.note}</p> : null}
    <p className="tool-metric-date">{metric.data_as_of
      ? <>{t('Datos de')} <DateTime value={metric.data_as_of} /></>
      : t('Sin fecha de datos')}</p>
    <details className="tool-metric-explain">
      <summary>{t('Cómo se calcula')}</summary>
      <p>{t(metric.description)}</p>
      <p className="tool-metric-formula">{metricFormula(metric)}</p>
      <p className="tool-metric-id"><code>{metric.id}</code></p>
    </details>
  </li>;
}

function MetricRecords({ selection, onUnauthorized }: { selection: Selection; onUnauthorized?: () => void }) {
  const [data, setData] = useState<InventoryMetricRecords | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    setData(null);
    setError('');
    fetchInventoryMetricRecords(selection.metric.id, selection.connectionId, controller.signal)
      .then((response) => { if (!controller.signal.aborted) setData(response); })
      .catch((caught) => {
        if (controller.signal.aborted) return;
        if (caught instanceof ApiError && caught.status === 401) { onUnauthorized?.(); return; }
        setError(caught instanceof Error ? caught.message : t('No se pudo cargar el detalle'));
      });
    return () => controller.abort();
  }, [selection, onUnauthorized]);

  const columns = data ? recordColumns(data.records) : [];
  return <section className="tool-metric-records" aria-label={t('Registros de {name}', { name: t(selection.metric.name) })}>
    <div className="activity-meta">
      <span><ListTree size={14} /> {t('Registros de {name}', { name: t(selection.metric.name) })}</span>
      {data ? <strong>{t('{count} registros', { count: formatNumber(data.total) })}</strong> : null}
    </div>
    {error ? <StateBlock variant="error" title={t('No se pudo cargar el detalle')} hint={error} compact />
      : !data ? <StateBlock variant="loading" title={t('Cargando registros…')} compact />
        : data.records.length === 0 ? <StateBlock variant="empty" title={data.metric.value === null ? t('Sin valor para detallar') : t('Ningún registro compone este valor')} compact />
          : <>
            {data.metric.partial ? <p className="tool-inventory-warning">{t('Publicación parcial: estos son los registros publicados; pueden faltar otros.')}</p> : null}
            <div className="tool-records-scroll">
              <table className="tool-records-table">
                <thead><tr><th>{t('ID externo')}</th>{columns.map((column) => <th key={column}>{column}</th>)}</tr></thead>
                <tbody>{data.records.map((record) => <tr key={record.external_id}>
                  <th scope="row"><code>{record.external_id}</code></th>
                  {columns.map((column) => <td key={column}>{record.attributes[column] ?? '—'}</td>)}
                </tr>)}</tbody>
              </table>
            </div>
            {data.truncated ? <p className="tool-inventory-warning">{t('Se muestran {shown} de {total} registros.', { shown: formatNumber(data.records.length), total: formatNumber(data.total) })}</p> : null}
          </>}
  </section>;
}

function ToolCard({ tool, selection, onSelect, onUnauthorized }: {
  tool: InventoryTool; selection: Selection | null; onSelect: (selection: Selection | null) => void; onUnauthorized?: () => void;
}) {
  const completeness = completenessBadge(tool.completeness);
  const lastRun = runStatusBadge(tool.last_run?.status);
  const active = selection?.connectionId === tool.connection_id ? selection : null;
  return <article className="activity-panel tool-inventory-card" aria-labelledby={`tool-${tool.connection_id}`}>
    <header className="tool-inventory-head">
      <div>
        <h2 id={`tool-${tool.connection_id}`}>{tool.name}</h2>
        <p>{[tool.vendor, tool.category].filter(Boolean).join(' · ')} · <code>{tool.connection_id}</code></p>
      </div>
      <dl className="tool-inventory-status">
        <div><dt>{t('Completitud')}</dt><dd><StatusBadge tone={completeness.tone}>{completeness.label}</StatusBadge></dd></div>
        <div><dt>{t('Última corrida')}</dt><dd><StatusBadge tone={lastRun.tone}>{lastRun.label}</StatusBadge> {tool.last_run?.completed_at ? <DateTime value={tool.last_run.completed_at} /> : null}</dd></div>
      </dl>
    </header>
    <ul className="tool-collections" aria-label={t('Colecciones')}>
      {tool.collections.map((collection) => {
        const badge = collectionStateBadge(collection);
        return <li key={collection.connection_id}>
          <code>{collection.collection}</code>
          <StatusBadge tone={badge.tone}>{badge.label}</StatusBadge>
          {collection.records !== undefined ? <span>{t('{count} registros', { count: formatNumber(collection.records) })}</span> : null}
          {collection.publication?.completed_at ? <DateTime value={collection.publication.completed_at} /> : null}
        </li>;
      })}
    </ul>
    {tool.metrics.length === 0
      ? <p className="tool-inventory-note">{t('Este conector no declara métricas.')}</p>
      : <ul className="tool-metrics" aria-label={t('Métricas de {name}', { name: tool.name })}>
        {tool.metrics.map((metric) => <MetricCard key={metric.id} metric={metric} selected={active?.metric.id === metric.id}
          onOpen={() => onSelect(active?.metric.id === metric.id ? null : { connectionId: tool.connection_id, metric })} />)}
      </ul>}
    {active ? <MetricRecords selection={active} onUnauthorized={onUnauthorized} /> : null}
  </article>;
}

export function ToolInventoryWorkspace({ onUnauthorized }: { onUnauthorized?: () => void }) {
  const [tools, setTools] = useState<InventoryTool[] | null>(null);
  const [error, setError] = useState('');
  const [selection, setSelection] = useState<Selection | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const response = await fetchInventoryTools(signal);
      if (signal?.aborted) return;
      setTools(response.tools);
    } catch (caught) {
      if (signal?.aborted) return;
      if (caught instanceof ApiError && caught.status === 401) { onUnauthorized?.(); return; }
      setError(caught instanceof Error ? caught.message : t('No se pudo cargar el inventario'));
    }
  }, [onUnauthorized]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  return <section className="tool-inventory">
    <div className="activity-panel tool-inventory-intro">
      <div className="activity-meta">
        <span><Boxes size={14} /> {t('Inventario de herramientas')}</span>
        {tools ? <strong>{t('{count} conexiones visibles', { count: tools.length })}</strong> : null}
      </div>
      <p className="tool-inventory-note">{t('Cada conector declara sus métricas. Se calculan sobre la última publicación de cada colección, con su fecha y el estado de la corrida. Sin datos publicados la métrica figura como no disponible, nunca como cero.')}</p>
    </div>
    {error ? <StateBlock variant="error" title={t('No se pudo cargar el inventario')} hint={error} />
      : tools === null ? <StateBlock variant="loading" title={t('Cargando inventario…')} />
        : tools.length === 0 ? <StateBlock variant="empty" title={t('No hay herramientas visibles')} hint={t('Conectá una herramienta o pedí permiso de lectura sobre una conexión existente.')} />
          : tools.map((tool) => <ToolCard key={tool.connection_id} tool={tool} selection={selection} onSelect={setSelection} onUnauthorized={onUnauthorized} />)}
  </section>;
}
