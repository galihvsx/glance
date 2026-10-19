// C27: shared frontend types for workspace initiatives. Mirrors the
// backend contract in docs/openapi.yaml (#/components/schemas/Initiative*)
// and internal/service/initiative.go.

import type { ProjectLabel } from "../../lib/types";

export type InitiativeStatus = "active" | "completed" | "archived";

export interface Initiative {
  id: string;
  workspace_id: string;
  name: string;
  description: string | null;
  status: InitiativeStatus;
  created_by?: string | null;
  project_count: number;
  created_at: string;
  updated_at: string;
}

export interface InitiativeProject {
  project_id: string;
  project_name: string;
  /** Workspace project-label chips (C76T0): [{id,name,color}], [] never
   *  null — shipped by GET .../initiatives/:id/projects in-query. */
  labels: ProjectLabel[];
  added_at: string;
}

export interface InitiativeProjectRollup {
  project_id: string;
  project_name: string;
  total: number;
  done_count: number;
  completion_pct: number;
  /** Issue counts by state group (triage/backlog/unstarted/started/completed/cancelled). */
  by_group: Record<string, number>;
}

export interface InitiativeRollup {
  initiative_id: string;
  total: number;
  done_count: number;
  completion_pct: number;
  by_group: Record<string, number>;
  projects: InitiativeProjectRollup[];
}
