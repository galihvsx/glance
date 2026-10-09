import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { api } from "../lib/api";
import { renderMarkdown } from "../lib/markdown";
import { Badge } from "../components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";

/**
 * Public share page (C4T5): renders the sanitized payload for a share
 * token at `/s/:token`. No auth layout, no login required — the token
 * is the only capability. Unknown/revoked/expired tokens 404.
 */

interface PublicLabel {
  name: string;
  color: string;
}

interface PublicIssue {
  display_id: string;
  name: string;
  description?: unknown;
  state: string;
  state_group: string;
  priority: number;
  labels: PublicLabel[];
  start_date?: string;
  target_date?: string;
  created_at: string;
  updated_at: string;
}

interface PublicPage {
  title: string;
  content: string;
  created_at: string;
  updated_at: string;
}

interface PublicShare {
  scope: "issue" | "page";
  issue?: PublicIssue;
  page?: PublicPage;
}

function descriptionText(desc: unknown): string {
  if (typeof desc === "string") return desc;
  if (desc && typeof desc === "object") {
    // TipTap-ish doc → plain text fallback.
    try {
      const doc = desc as { content?: { content?: { text?: string }[] }[] };
      const parts: string[] = [];
      for (const block of doc.content ?? []) {
        for (const node of block.content ?? []) {
          if (node.text) parts.push(node.text);
        }
        parts.push("\n");
      }
      return parts.join("").trim();
    } catch {
      return "";
    }
  }
  return "";
}

export default function PublicShare() {
  const { token = "" } = useParams<{ token: string }>();

  const query = useQuery({
    queryKey: ["public-share", token],
    queryFn: () =>
      api.get<PublicShare>(`/api/v1/public/s/${encodeURIComponent(token)}`),
    retry: false,
  });

  return (
    <div className="mx-auto max-w-3xl px-4 py-10">
      <header className="mb-6 flex items-center gap-2">
        <span className="text-lg font-bold">glance</span>
        <span className="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground">
          shared view
        </span>
      </header>

      {query.isLoading ? (
        <p className="text-muted-foreground">Loading…</p>
      ) : query.isError || !query.data ? (
        <Card>
          <CardHeader>
            <CardTitle>Link unavailable</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              This link is invalid, expired, or was revoked.
            </p>
          </CardContent>
        </Card>
      ) : query.data.scope === "issue" && query.data.issue ? (
        <IssueView issue={query.data.issue} />
      ) : query.data.scope === "page" && query.data.page ? (
        <PageView page={query.data.page} />
      ) : (
        <p className="text-muted-foreground">Nothing to show.</p>
      )}

      <footer className="mt-10 text-center text-xs text-muted-foreground">
        Shared from glance — sign in to collaborate.
      </footer>
    </div>
  );
}

function IssueView({ issue }: { issue: PublicIssue }) {
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono">{issue.display_id}</span>
          <Badge variant="secondary">{issue.state}</Badge>
          {issue.labels.map((l) => (
            <span
              key={l.name}
              className="rounded-full px-2 py-0.5 text-xs"
              style={{ backgroundColor: `${l.color}22`, color: l.color }}
            >
              {l.name}
            </span>
          ))}
        </div>
        <CardTitle className="text-xl">{issue.name}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {descriptionText(issue.description) && (
          <p className="whitespace-pre-wrap text-sm">
            {descriptionText(issue.description)}
          </p>
        )}
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <div>
            <dt className="text-muted-foreground">Priority</dt>
            <dd>P{issue.priority}</dd>
          </div>
          {issue.target_date && (
            <div>
              <dt className="text-muted-foreground">Due</dt>
              <dd>{new Date(issue.target_date).toLocaleDateString()}</dd>
            </div>
          )}
        </dl>
      </CardContent>
    </Card>
  );
}

function PageView({ page }: { page: PublicPage }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-xl">{page.title}</CardTitle>
      </CardHeader>
      <CardContent>
        <div
          className="wiki-content"
          dangerouslySetInnerHTML={{ __html: renderMarkdown(page.content) }}
        />
      </CardContent>
    </Card>
  );
}
