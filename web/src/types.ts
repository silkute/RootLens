export type {
  Capability,
  Entity,
  Host,
  Evidence,
  Contributor,
  Incident,
  MetricPoint,
  MetricSeries,
  MetricsResponse,
  Event,
  Retention,
} from "./contracts.generated";
import type { Entity } from "./contracts.generated";
export interface Container {
  entity: Entity;
  values: Record<string, number>;
  at: string;
}
