import { Copy, GitBranch, Hash, Link2 } from "lucide-react";
import { buildBranchName } from "../../lib/branchName";
import { copyText } from "../../lib/clipboard";
import { useShortcutAction } from "../../lib/shortcuts";
import type { Issue } from "../../lib/types";
import { Button } from "../ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Kbd } from "../ui/kbd";
import { toast } from "../ui/toast";

type CopyKind = "branch" | "url" | "id";

/**
 * Dev quick actions: a "Copy" dropdown in the issue detail header with
 * "Copy branch name", "Copy issue URL" and "Copy ID". Each action copies
 * to the clipboard and confirms with a toast. The same actions are
 * reachable via the `c b` / `c u` / `c i` keyboard chords (registered here,
 * so they only fire while this component is mounted — i.e. on issue
 * detail).
 */
export default function IssueCopyMenu({
  slug,
  identifier,
  uuid,
  issue,
}: {
  /** Workspace slug (route param). */
  slug: string;
  /** Project identifier (route param). */
  identifier: string;
  /** Issue UUID (route param). */
  uuid: string;
  issue: Issue;
}) {
  async function doCopy(kind: CopyKind) {
    const value =
      kind === "branch"
        ? buildBranchName(issue.display_id, issue.name)
        : kind === "url"
          ? `${window.location.origin}/w/${slug}/p/${identifier}/i/${uuid}`
          : issue.display_id;
    const label =
      kind === "branch" ? "Branch name" : kind === "url" ? "Issue URL" : "Issue ID";
    if (await copyText(value)) {
      toast.add({
        title: `${label} copied`,
        description: value,
        type: "success",
      });
    } else {
      toast.add({
        title: `Could not copy ${label.toLowerCase()}`,
        type: "error",
      });
    }
  }

  useShortcutAction("copy-branch", () => void doCopy("branch"));
  useShortcutAction("copy-url", () => void doCopy("url"));
  useShortcutAction("copy-id", () => void doCopy("id"));

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            title="Copy issue details"
          >
            <Copy className="h-3.5 w-3.5" />
            Copy
          </Button>
        }
      />
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => void doCopy("branch")}>
          <GitBranch className="h-4 w-4" />
          Copy branch name
          <span className="ml-auto flex items-center gap-0.5 pl-4">
            <Kbd>c</Kbd>
            <Kbd>b</Kbd>
          </span>
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => void doCopy("url")}>
          <Link2 className="h-4 w-4" />
          Copy issue URL
          <span className="ml-auto flex items-center gap-0.5 pl-4">
            <Kbd>c</Kbd>
            <Kbd>u</Kbd>
          </span>
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => void doCopy("id")}>
          <Hash className="h-4 w-4" />
          Copy ID
          <span className="ml-auto flex items-center gap-0.5 pl-4">
            <Kbd>c</Kbd>
            <Kbd>i</Kbd>
          </span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
