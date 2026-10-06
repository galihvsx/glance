// Shared domain types mirroring the backend JSON shapes (snake_case).

export interface Workspace {
  id: string;
  slug: string;
  name: string;
  /** 20 = admin, 15 = member, 5 = guest */
  role: number;
  created_at: string;
  updated_at: string;
}

export interface Project {
  id: string;
  workspace_id: string;
  identifier: string;
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface IssueState {
  id: string;
  project_id: string;
  name: string;
  group: string;
  color: string;
  sequence: number;
  created_at: string;
  updated_at: string;
}

/** Human label for a workspace membership role. */
export function roleLabel(role: number): string {
  switch (role) {
    case 20:
      return "Admin";
    case 15:
      return "Member";
    case 5:
      return "Guest";
    default:
      return `Role ${role}`;
  }
}
