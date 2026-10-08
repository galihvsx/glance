import { Link, useParams } from "react-router-dom";
import ThemeToggle from "../components/ThemeToggle";
import IssueDetailContent from "../components/issue/IssueDetailContent";

/** Full-page issue detail. The body is shared with the peek drawer. */
export default function IssueDetail() {
  const { slug = "", identifier = "", uuid = "" } = useParams<{
    slug: string;
    identifier: string;
    uuid: string;
  }>();

  return (
    <div className="mx-auto w-full max-w-5xl p-6">
      <Link
        to={`/w/${slug}/p/${identifier}`}
        className="text-xs text-muted-foreground hover:underline"
      >
        ← Issues
      </Link>
      <IssueDetailContent
        slug={slug}
        identifier={identifier}
        uuid={uuid}
        headerActions={<ThemeToggle />}
      />
    </div>
  );
}
