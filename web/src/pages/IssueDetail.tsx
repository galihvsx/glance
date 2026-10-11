import { useNavigate, useParams } from "react-router-dom";
import IssueDetailContent from "../components/issue/IssueDetailContent";

/**
 * Full-page issue detail. The body is shared with the peek drawer; the
 * 46px toolbar's close button navigates back to the project (C19T3: the
 * audit wants the same toolbar on the full page, close → back).
 */
export default function IssueDetail() {
  const navigate = useNavigate();
  const { slug = "", identifier = "", uuid = "" } = useParams<{
    slug: string;
    identifier: string;
    uuid: string;
  }>();

  return (
    <div className="flex h-full min-h-0 flex-col bg-canvas">
      <IssueDetailContent
        slug={slug}
        identifier={identifier}
        uuid={uuid}
        fillHeight
        onClose={() => navigate(`/w/${slug}/p/${identifier}`)}
      />
    </div>
  );
}
