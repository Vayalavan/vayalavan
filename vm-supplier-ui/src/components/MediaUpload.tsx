import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type DragEvent,
  type ReactElement,
} from "react";
import { ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import type { MediaKind, PresignResponse } from "../lib/types.js";

/** Must match allowedMediaTypes in vm-catalog-api (CLAUDE.md §6.6). */
const ACCEPTED_IMAGE = ["image/jpeg", "image/png", "image/webp"];
const ACCEPTED_VIDEO = ["video/mp4", "video/quicktime", "video/webm"];
const ACCEPTED = [...ACCEPTED_IMAGE, ...ACCEPTED_VIDEO];

/** Must match MAX_IMAGE_BYTES / MAX_VIDEO_BYTES / MAX_MEDIA_PER_PRODUCT. */
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const MAX_VIDEO_BYTES = 50 * 1024 * 1024;
const MAX_ITEMS = 8;

/**
 * One gallery item as the form holds it.
 *
 * `previewUrl` is either a presigned GET from the server or a local object URL
 * for a file uploaded in this session; `isLocal` says which, because only the
 * local ones need revoking.
 */
export interface MediaItem {
  kind: MediaKind;
  objectKey: string;
  contentType: string;
  previewUrl: string | null;
  isLocal: boolean;
}

interface MediaUploadProps {
  value: MediaItem[];
  onChange: (items: MediaItem[]) => void;
  /**
   * The heading above the gallery. Defaulted rather than required, because
   * every caller but one is a size code's own gallery — the exception is the
   * product-level bucket, where a card already says what these pictures are
   * and a second "Photos and videos" would be two headings for one control.
   * Pass "" to render none.
   */
  label?: string;
  /**
   * What the first photo means here. Inside a grade it is that grade's cover;
   * in the product-level bucket it is only a cover when the grade has no
   * picture of its own, so the caller says so rather than this component
   * asserting something untrue.
   */
  coverHint?: string;
  /**
   * Whether the first image wears a "Cover" badge. False for the
   * product-level bucket, where it is a cover only for grades that have no
   * picture of their own — a badge would state that as a fact.
   */
  coverBadge?: boolean;
}

function megabytes(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * Drag-and-drop gallery for a product's images and videos.
 *
 * Files go straight from the browser to object storage via presigned PUTs —
 * they never pass through the gateway or the API (CLAUDE.md §6.6). What comes
 * back to the form is the object KEY, which is what gets stored.
 *
 * Order is the gallery order, and the FIRST IMAGE is the cover shown on
 * catalogue cards and order history. That is stated in the UI rather than
 * inferred, because it is the one thing about this control a grower has to
 * understand to get the listing they want.
 */
export function MediaUpload({
  value,
  onChange,
  label = "Photos and videos",
  coverHint = "The first photo is the cover customers see on the shop page.",
  coverBadge = true,
}: MediaUploadProps): ReactElement {
  const [dragging, setDragging] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Object URLs are a memory leak if not revoked. Only the local ones are
  // ours to revoke; the presigned ones are plain URLs.
  const localUrls = useRef<Set<string>>(new Set());
  useEffect(() => {
    const urls = localUrls.current;
    return () => {
      for (const url of urls) URL.revokeObjectURL(url);
      urls.clear();
    };
  }, []);

  const uploadAll = useCallback(
    async (files: File[]) => {
      setError(null);

      const room = MAX_ITEMS - value.length;
      if (room <= 0) {
        setError(`A product can have at most ${MAX_ITEMS} images and videos.`);
        return;
      }
      if (files.length > room) {
        setError(
          `Only ${room} more ${room === 1 ? "file fits" : "files fit"} — the limit is ${MAX_ITEMS}.`,
        );
        files = files.slice(0, room);
      }

      const added: MediaItem[] = [];
      for (const file of files) {
        // Checked here as well as on the server: the point is instant
        // feedback, not security. The presigned URL binds the content type,
        // so the server remains the authority.
        if (!ACCEPTED.includes(file.type)) {
          setError("Choose a JPEG, PNG or WebP image, or an MP4, MOV or WebM video.");
          break;
        }
        const isVideo = ACCEPTED_VIDEO.includes(file.type);
        const limit = isVideo ? MAX_VIDEO_BYTES : MAX_IMAGE_BYTES;
        if (file.size > limit) {
          setError(
            `${file.name} is ${megabytes(file.size)}. The limit for a ${
              isVideo ? "video" : "image"
            } is ${megabytes(limit)}.`,
          );
          break;
        }

        const preview = URL.createObjectURL(file);
        localUrls.current.add(preview);

        try {
          setProgress(0);
          const presigned = await api.post<PresignResponse>("/uploads/presign", {
            content_type: file.type,
            size_bytes: file.size,
            filename: file.name,
          });

          await putWithProgress(presigned, file, setProgress);
          added.push({
            kind: presigned.kind,
            objectKey: presigned.key,
            contentType: presigned.content_type,
            previewUrl: preview,
            isLocal: true,
          });
        } catch (err) {
          URL.revokeObjectURL(preview);
          localUrls.current.delete(preview);
          setError(err instanceof ApiError ? err.message : "Upload failed. Please try again.");
          break;
        } finally {
          setProgress(null);
        }
      }

      if (added.length > 0) onChange([...value, ...added]);
    },
    [onChange, value],
  );

  function onDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragging(false);
    const files = Array.from(event.dataTransfer.files);
    if (files.length > 0) void uploadAll(files);
  }

  function remove(index: number) {
    const item = value[index];
    if (item?.isLocal && item.previewUrl) {
      URL.revokeObjectURL(item.previewUrl);
      localUrls.current.delete(item.previewUrl);
    }
    onChange(value.filter((_, i) => i !== index));
  }

  /** Moves one item by `delta` places, clamped to the ends. */
  function move(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= value.length) return;
    const next = [...value];
    const [item] = next.splice(index, 1);
    next.splice(target, 0, item!);
    onChange(next);
  }

  const uploading = progress !== null;
  const coverIndex = value.findIndex((item) => item.kind === "image");

  return (
    <div>
      {label !== "" && (
        <span className="block text-sm font-medium text-primary-900">{label}</span>
      )}
      <p className={`text-xs text-primary-900/50 ${label !== "" ? "mt-0.5" : ""}`}>
        Up to {MAX_ITEMS} files. JPEG, PNG or WebP images up to {megabytes(MAX_IMAGE_BYTES)}, MP4,
        MOV or WebM videos up to {megabytes(MAX_VIDEO_BYTES)}.
      </p>

      {value.length > 0 && (
        <ul className="mt-2 grid grid-cols-2 gap-3 sm:grid-cols-4">
          {value.map((item, index) => (
            <li
              key={item.objectKey}
              className="relative overflow-hidden rounded-card border border-surface-border bg-surface-raised"
            >
              <div className="aspect-square w-full bg-surface-sunken">
                {item.previewUrl ? (
                  item.kind === "video" ? (
                    // No autoplay: a form with several clips playing at once is
                    // unusable, and metadata alone is enough for a poster frame.
                    <video
                      src={item.previewUrl}
                      className="h-full w-full object-cover"
                      preload="metadata"
                      muted
                      controls
                    />
                  ) : (
                    <img
                      src={item.previewUrl}
                      alt=""
                      className="h-full w-full object-cover"
                    />
                  )
                ) : (
                  <div className="flex h-full w-full items-center justify-center text-xs text-primary-900/40">
                    {item.kind === "video" ? "Video" : "Image"}
                  </div>
                )}
              </div>

              {coverBadge && index === coverIndex && (
                <span className="absolute left-1 top-1 rounded-badge bg-primary-700 px-1.5 py-0.5 text-[10px] font-medium text-surface">
                  Cover
                </span>
              )}
              {item.kind === "video" && (
                <span className="absolute right-1 top-1 rounded-badge bg-primary-900/70 px-1.5 py-0.5 text-[10px] font-medium text-surface">
                  Video
                </span>
              )}

              <div className="flex items-center justify-between gap-1 border-t border-surface-border px-1.5 py-1">
                <div className="flex gap-0.5">
                  <button
                    type="button"
                    onClick={() => move(index, -1)}
                    disabled={index === 0}
                    aria-label={`Move ${index + 1} earlier`}
                    className="rounded px-1.5 py-0.5 text-xs text-primary-700 disabled:opacity-30"
                  >
                    ←
                  </button>
                  <button
                    type="button"
                    onClick={() => move(index, 1)}
                    disabled={index === value.length - 1}
                    aria-label={`Move ${index + 1} later`}
                    className="rounded px-1.5 py-0.5 text-xs text-primary-700 disabled:opacity-30"
                  >
                    →
                  </button>
                </div>
                <button
                  type="button"
                  onClick={() => remove(index)}
                  className="text-xs font-medium text-accent-700 underline"
                >
                  Remove
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      <div
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        className={`mt-2 rounded-card border-2 border-dashed p-4 text-center transition-colors ${
          dragging ? "border-primary-500 bg-primary-50" : "border-surface-border bg-surface-raised"
        }`}
      >
        <p className="text-sm text-primary-900/70">
          Drag files here, or{" "}
          <button
            type="button"
            onClick={() => inputRef.current?.click()}
            className="font-medium text-primary-700 underline"
          >
            choose files
          </button>
          .
        </p>
        {value.length > 0 && (
          <p className="mt-0.5 text-xs text-primary-900/50">
            {coverIndex === -1
              ? "Add at least one photo — a video alone leaves the listing without a cover picture."
              : coverHint}
          </p>
        )}

        {uploading && (
          <div className="mt-2">
            {/* Native progress semantics, so assistive tech announces it. */}
            <div
              role="progressbar"
              aria-valuenow={progress}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label="Upload progress"
              className="h-2 w-full overflow-hidden rounded-full bg-surface-sunken"
            >
              <div
                className="h-full bg-primary-500 transition-[width] duration-150"
                style={{ width: `${progress}%` }}
              />
            </div>
            <p className="mt-1 text-xs text-primary-900/60">Uploading… {progress}%</p>
          </div>
        )}

        {error && (
          <p role="alert" className="mt-2 text-sm text-accent-800">
            {error}
          </p>
        )}

        <input
          ref={inputRef}
          type="file"
          multiple
          accept={ACCEPTED.join(",")}
          className="sr-only"
          onChange={(e) => {
            const files = Array.from(e.target.files ?? []);
            if (files.length > 0) void uploadAll(files);
            // Reset so choosing the same file twice still fires a change.
            e.target.value = "";
          }}
        />
      </div>
    </div>
  );
}

/**
 * PUTs the file to the presigned URL, reporting progress.
 *
 * XMLHttpRequest rather than fetch: fetch still cannot report upload progress,
 * and a supplier on a slow connection uploading a 50 MB clip needs to see that
 * something is happening.
 *
 * Deliberately sends NO Authorization header — the signature IS the
 * authorisation, and an extra header would change the signed request.
 */
function putWithProgress(
  presigned: PresignResponse,
  file: File,
  onProgress: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", presigned.upload_url, true);
    // Must match the content type bound into the signature, byte for byte.
    xhr.setRequestHeader("Content-Type", presigned.content_type);

    xhr.upload.addEventListener("progress", (event) => {
      if (event.lengthComputable) {
        onProgress(Math.round((event.loaded / event.total) * 100));
      }
    });

    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        onProgress(100);
        resolve();
        return;
      }
      reject(new ApiError(xhr.status, "UPLOAD_FAILED", "The file could not be uploaded."));
    });
    xhr.addEventListener("error", () =>
      reject(new ApiError(0, "NETWORK_ERROR", "The file could not be uploaded.")),
    );
    xhr.addEventListener("abort", () =>
      reject(new ApiError(0, "UPLOAD_ABORTED", "The upload was cancelled.")),
    );

    xhr.send(file);
  });
}
