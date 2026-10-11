import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Paperclip, Trash2, UploadCloud } from "lucide-react";

import { ApiError, api } from "../../lib/api";
import { formatBytes, formatDateTime } from "../../lib/format";
import type { Attachment } from "../../lib/types";
import { Button, buttonVariants } from "../ui/button";

/**
 * Attachments section for the issue detail sidebar (C4T2).
 *
 * Upload via drag-and-drop or the file picker (multipart POST), list
 * with size + uploader + created date, download through a plain anchor
 * (same-origin cookie auth; the server sets inline vs. attachment
 * disposition), delete behind a confirm.
 */
export default function Attachments({
  slug,
  identifier,
  uuid,
  /** C19T3: increment to open the file picker from the detail action
   *  toolbar. */
  pickSignal,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  pickSignal?: number;
}) {
  const queryClient = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);

  // C19T3: external "Attach" trigger from the action toolbar.
  useEffect(() => {
    if (pickSignal) fileInput.current?.click();
  }, [pickSignal]);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/attachments`;
  const key = ["attachments", slug, identifier, uuid];

  const listQuery = useQuery({
    queryKey: key,
    queryFn: () => api.get<{ attachments: Attachment[] }>(base),
  });
  const attachments = listQuery.data?.attachments ?? [];

  const uploadMutation = useMutation({
    mutationFn: (files: FileList | File[]) => {
      const list = Array.from(files);
      // One request per file: the API is single-file per POST and this
      // keeps per-file errors attributable.
      return Promise.all(
        list.map((f) => {
          const fd = new FormData();
          fd.append("file", f, f.name);
          return api.postForm<Attachment>(base, fd);
        }),
      );
    },
    onSuccess: () => {
      setUploadError(null);
      void queryClient.invalidateQueries({ queryKey: key });
    },
    onError: (e) => {
      setUploadError(
        e instanceof ApiError && e.status === 413
          ? "File exceeds the server's per-file size limit."
          : `Upload failed: ${e instanceof Error ? e.message : "unknown error"}`,
      );
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.del(`${base}/${encodeURIComponent(id)}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });

  const acceptFiles = (files: FileList | null) => {
    if (!files || files.length === 0) return;
    setUploadError(null);
    uploadMutation.mutate(files);
  };

  const confirmDelete = (a: Attachment) => {
    if (!window.confirm(`Delete attachment "${a.filename}"?`)) return;
    deleteMutation.mutate(a.id);
  };

  return (
    <section aria-label="Attachments" className="mt-[18px]">
      <h2 className="mb-[9px] flex items-center gap-2 text-[12px] leading-[1.5] font-semibold text-ink">
        <Paperclip className="h-3.5 w-3.5 text-ink-muted" aria-hidden />
        Attachments
        {attachments.length > 0 && (
          <span className="text-[10px] font-normal text-ink-muted">
            {attachments.length}
          </span>
        )}
      </h2>
      <div className="space-y-3">
        {/* Dropzone */}
        <div
          role="button"
          tabIndex={0}
          aria-label="Upload attachments"
          onClick={() => fileInput.current?.click()}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") fileInput.current?.click();
          }}
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            acceptFiles(e.dataTransfer.files);
          }}
          className={`flex cursor-pointer flex-col items-center justify-center gap-1 rounded-md border border-dashed px-4 py-5 text-center transition-colors ${
            dragging
              ? "border-accent-blue bg-accent-blue/5"
              : "border-ink-muted/25 hover:border-ink-muted/50"
          }`}
        >
          <UploadCloud
            className="h-5 w-5 text-ink-muted"
            aria-hidden
          />
          <p className="text-xs text-ink-muted">
            {uploadMutation.isPending
              ? "Uploading…"
              : "Drop files here or click to browse"}
          </p>
          <input
            ref={fileInput}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => {
              acceptFiles(e.target.files);
              e.target.value = "";
            }}
          />
        </div>
        {uploadError && (
          <p className="text-xs text-accent-red" role="alert">
            {uploadError}
          </p>
        )}

        {/* List */}
        {listQuery.isLoading ? (
          <p className="text-xs text-ink-muted">Loading…</p>
        ) : attachments.length === 0 ? (
          <p className="text-xs text-ink-muted">No attachments yet.</p>
        ) : (
          <ul className="space-y-2">
            {attachments.map((a) => (
              <li
                key={a.id}
                className="flex items-start justify-between gap-2 rounded-md border px-2.5 py-2"
              >
                <div className="min-w-0">
                  <a
                    href={`${base}/${encodeURIComponent(a.id)}`}
                    className="block truncate text-xs font-medium hover:underline"
                    title={a.filename}
                  >
                    {a.filename}
                  </a>
                  <p className="mt-0.5 text-[11px] text-ink-muted">
                    {formatBytes(a.size_bytes)} ·{" "}
                    {a.uploaded_by.name || a.uploaded_by.email} ·{" "}
                    {formatDateTime(a.created_at)}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <a
                    href={`${base}/${encodeURIComponent(a.id)}`}
                    download
                    title="Download"
                    className={buttonVariants({ variant: "ghost", size: "icon" })}
                  >
                    <Download className="h-3.5 w-3.5" aria-hidden />
                    <span className="sr-only">Download {a.filename}</span>
                  </a>
                  <Button
                    variant="ghost"
                    size="icon"
                    title="Delete"
                    disabled={deleteMutation.isPending}
                    onClick={() => confirmDelete(a)}
                  >
                    <Trash2 className="h-3.5 w-3.5" aria-hidden />
                    <span className="sr-only">Delete {a.filename}</span>
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
