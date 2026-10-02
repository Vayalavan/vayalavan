import { useRef, useState, type DragEvent, type ReactElement } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, ApiError } from "@vayal/ui-kit";
import { api, getAccessToken } from "../lib/api.js";
import { config } from "../lib/config.js";
import { formatPaise, formatGrams } from "../lib/money.js";
import type { ImportPreview, ImportResult } from "../lib/types.js";

type Step = "upload" | "preview" | "done";

/**
 * The CSV import wizard: upload -> preview -> confirm -> result.
 *
 * The two server calls mirror CLAUDE.md §6.5 exactly. Uploading only
 * validates and stores a report; nothing reaches the catalogue until the
 * supplier presses Import, so a bad file costs them nothing but a re-upload.
 */
export function ImportPage(): ReactElement {
  const [step, setStep] = useState<Step>("upload");
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const upload = useMutation<ImportPreview, ApiError, File>({
    mutationFn: (file) => uploadCSV(file),
    onSuccess: (data) => {
      setPreview(data);
      setStep("preview");
      setError(null);
    },
    onError: (err) => {
      // The server's per-file message ("missing required column: name") is
      // written for suppliers, so show it as-is.
      const detail = err.details?.["file"];
      setError(typeof detail === "string" ? detail : err.message);
    },
  });

  const commit = useMutation<ImportResult, ApiError>({
    mutationFn: () => api.post<ImportResult>(`/imports/${preview?.import_id}/commit`),
    onSuccess: (data) => {
      setResult(data);
      setStep("done");
      void queryClient.invalidateQueries({ queryKey: ["products"] });
    },
    onError: (err) => setError(err.message),
  });

  function handleFile(file: File | undefined) {
    if (!file) return;
    setError(null);
    upload.mutate(file);
  }

  return (
    <>
      <PageHeading
        title="Import products from CSV"
        description="Upload a spreadsheet to add many products at once."
      />

      <Stepper step={step} />

      {error && (
        <Card className="mb-4">
          <p role="alert" className="text-sm text-accent-800">
            {error}
          </p>
        </Card>
      )}

      {step === "upload" && (
        <Card>
          <div
            onDragOver={(e) => {
              e.preventDefault();
              setDragging(true);
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e: DragEvent<HTMLDivElement>) => {
              e.preventDefault();
              setDragging(false);
              handleFile(e.dataTransfer.files[0]);
            }}
            className={`flex flex-col items-center gap-3 rounded-card border-2 border-dashed px-4 py-10 text-center transition-colors ${
              dragging ? "border-primary-500 bg-primary-50" : "border-surface-border"
            }`}
          >
            <p className="text-primary-900">
              Drag your CSV here, or{" "}
              <button
                type="button"
                onClick={() => inputRef.current?.click()}
                className="font-medium text-primary-700 underline"
              >
                choose a file
              </button>
            </p>
            <p className="text-xs text-primary-900/50">
              Up to 5 MB and 2000 rows. Nothing is saved until you confirm.
            </p>
            {upload.isPending && (
              <p className="text-sm text-primary-900/70">Checking your file…</p>
            )}

            <input
              ref={inputRef}
              type="file"
              accept=".csv,text/csv"
              className="sr-only"
              onChange={(e) => {
                handleFile(e.target.files?.[0]);
                e.target.value = "";
              }}
            />
          </div>

          <div className="mt-4 rounded-card bg-surface-sunken p-3 text-sm">
            <p className="font-medium text-primary-900">Not sure about the format?</p>
            <p className="mt-1 text-primary-900/70">
              Columns: <code>name, type, grade, description, unit_label, weight_grams,
              price_rupees, image_url, video_url</code>. Rows sharing a name and grade
              become one product with several units — and every different{" "}
              <code>image_url</code> and <code>video_url</code> across those rows joins
              that product's gallery, so repeat the row to add a second photo.
            </p>
            <button
              type="button"
              onClick={() => void downloadTemplate()}
              className="mt-2 font-medium text-primary-700 underline"
            >
              Download the template
            </button>
          </div>
        </Card>
      )}

      {step === "preview" && preview && (
        <PreviewStep
          preview={preview}
          committing={commit.isPending}
          onBack={() => {
            setPreview(null);
            setError(null);
            setStep("upload");
          }}
          onConfirm={() => commit.mutate()}
        />
      )}

      {step === "done" && result && (
        <Card>
          <h2 className="text-lg font-semibold text-primary-900">Import complete</h2>
          <dl className="mt-3 grid gap-2 sm:grid-cols-3">
            <Stat label="Products created" value={result.products_created} />
            <Stat label="Rows imported" value={result.rows_imported} />
            <Stat label="Rows skipped" value={result.rows_skipped} />
          </dl>
          <p className="mt-4 text-sm text-primary-900/70">
            Imported products start as <strong>drafts</strong> so you can review them
            before they go on sale.
          </p>
          <div className="mt-4 flex gap-2">
            <button
              onClick={() => navigate("/products")}
              className="rounded-card bg-primary-600 px-4 py-2 font-medium text-white"
            >
              Review products
            </button>
            <button
              onClick={() => {
                setStep("upload");
                setPreview(null);
                setResult(null);
              }}
              className="rounded-card border border-surface-border px-4 py-2 font-medium text-primary-800"
            >
              Import another file
            </button>
          </div>
        </Card>
      )}
    </>
  );
}

function Stepper({ step }: { step: Step }): ReactElement {
  const steps: { key: Step; label: string }[] = [
    { key: "upload", label: "1. Upload" },
    { key: "preview", label: "2. Review" },
    { key: "done", label: "3. Done" },
  ];
  const activeIndex = steps.findIndex((s) => s.key === step);

  return (
    <ol className="mb-4 flex flex-wrap gap-2 text-sm">
      {steps.map((s, index) => (
        <li
          key={s.key}
          aria-current={index === activeIndex ? "step" : undefined}
          className={`rounded-full px-3 py-1 ${
            index === activeIndex
              ? "bg-primary-600 text-white"
              : index < activeIndex
                ? "bg-primary-100 text-primary-800"
                : "bg-surface-sunken text-primary-900/50"
          }`}
        >
          {s.label}
        </li>
      ))}
    </ol>
  );
}

function PreviewStep({
  preview,
  committing,
  onBack,
  onConfirm,
}: {
  preview: ImportPreview;
  committing: boolean;
  onBack: () => void;
  onConfirm: () => void;
}): ReactElement {
  const hasValid = preview.valid_rows > 0;

  return (
    <>
      <Card className="mb-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="font-semibold text-primary-900">{preview.filename}</h2>
            <p className="mt-1 text-sm text-primary-900/70">
              {preview.valid_rows} of {preview.total_rows} rows are ready to import
              {preview.error_rows > 0 && `, ${preview.error_rows} have problems`}.
            </p>
          </div>
          <dl className="flex gap-4">
            <Stat label="Products" value={preview.products.length} />
            <Stat label="Valid" value={preview.valid_rows} />
            <Stat label="Errors" value={preview.error_rows} />
          </dl>
        </div>

        {preview.error_rows > 0 && (
          <p className="mt-3 rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800">
            Rows with problems are skipped. Fix them in your spreadsheet and upload
            again if you want them included.
          </p>
        )}
      </Card>

      <Card className="mb-4">
        <h3 className="mb-3 font-semibold text-primary-900">Rows</h3>
        {/* Wide table scrolls inside its own container so the page never
            scrolls sideways on a phone. */}
        <div className="-mx-2 overflow-x-auto">
          <table className="w-full min-w-[36rem] text-left text-sm">
            <thead>
              <tr className="border-b border-surface-border text-xs uppercase tracking-wide text-primary-900/60">
                <th className="px-2 py-2">Line</th>
                <th className="px-2 py-2">Name</th>
                <th className="px-2 py-2">Unit</th>
                <th className="px-2 py-2">Price</th>
                <th className="px-2 py-2">Status</th>
              </tr>
            </thead>
            <tbody>
              {preview.rows.map((row) => (
                <tr
                  key={row.line}
                  className={`border-b border-surface-border/60 align-top ${
                    row.status === "error" ? "bg-accent-50/60" : ""
                  }`}
                >
                  <td className="px-2 py-2 tabular-nums text-primary-900/60">{row.line}</td>
                  <td className="px-2 py-2">{row.parsed?.name ?? "—"}</td>
                  <td className="px-2 py-2">
                    {row.parsed
                      ? `${row.parsed.unit_label} (${formatGrams(row.parsed.weight_grams)})`
                      : "—"}
                  </td>
                  <td className="px-2 py-2">
                    {row.parsed ? formatPaise(row.parsed.price_paise) : "—"}
                  </td>
                  <td className="px-2 py-2">
                    {row.status === "valid" ? (
                      <span className="rounded-full bg-primary-100 px-2 py-0.5 text-xs text-primary-800">
                        Ready
                      </span>
                    ) : (
                      // Errors sit inline on the row they belong to, so the
                      // supplier can map each one to a line in their file.
                      <ul className="space-y-0.5 text-xs text-accent-800">
                        {row.errors?.map((message) => (
                          <li key={message}>• {message}</li>
                        ))}
                      </ul>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      {preview.products.length > 0 && (
        <Card className="mb-4">
          <h3 className="mb-2 font-semibold text-primary-900">
            Products that will be created
          </h3>
          <ul className="space-y-2 text-sm">
            {preview.products.map((product) => (
              <li key={`${product.name}-${product.grade}`} className="rounded-card bg-surface-sunken p-3">
                <span className="font-medium text-primary-900">{product.name}</span>
                {product.grade && (
                  <span className="ml-2 text-primary-900/60">Grade {product.grade}</span>
                )}
                <span className="ml-2 capitalize text-primary-900/60">{product.type}</span>
                <ul className="mt-1 flex flex-wrap gap-x-4 text-primary-900/80">
                  {product.units.map((unit) => (
                    <li key={unit.weight_grams}>
                      {unit.label} — {formatPaise(unit.price_paise)}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        </Card>
      )}

      <div className="flex flex-wrap gap-2">
        <button
          onClick={onConfirm}
          disabled={!hasValid || committing}
          className="rounded-card bg-primary-600 px-5 py-2.5 font-medium text-white hover:bg-primary-700 disabled:opacity-50"
        >
          {committing
            ? "Importing…"
            : `Import ${preview.valid_rows} row${preview.valid_rows === 1 ? "" : "s"}`}
        </button>
        <button
          onClick={onBack}
          disabled={committing}
          className="rounded-card border border-surface-border px-5 py-2.5 font-medium text-primary-800"
        >
          Choose a different file
        </button>
        <Link
          to="/products"
          className="rounded-card px-5 py-2.5 font-medium text-primary-800/70"
        >
          Cancel
        </Link>
      </div>
    </>
  );
}

function Stat({ label, value }: { label: string; value: number }): ReactElement {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wide text-primary-900/50">{label}</dt>
      <dd className="text-lg font-semibold tabular-nums text-primary-900">{value}</dd>
    </div>
  );
}

/**
 * Uploads the CSV as multipart/form-data.
 *
 * Uses fetch directly rather than the shared JSON client: this is the one
 * request in the app with a non-JSON body. The Content-Type header is
 * deliberately NOT set — the browser must add it itself so it carries the
 * multipart boundary.
 */
async function uploadCSV(file: File): Promise<ImportPreview> {
  const form = new FormData();
  form.append("file", file);

  const token = getAccessToken();
  const response = await fetch(`${config.apiBaseUrl}/imports`, {
    method: "POST",
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    body: form,
    credentials: "include",
  });

  const text = await response.text();
  const body: unknown = text ? JSON.parse(text) : {};

  if (!response.ok) {
    const envelope = body as { error?: { code?: string; message?: string; details?: Record<string, unknown> } };
    throw new ApiError(
      response.status,
      envelope.error?.code ?? "UPLOAD_FAILED",
      envelope.error?.message ?? "That file could not be imported.",
      { details: envelope.error?.details },
    );
  }
  return body as ImportPreview;
}

/** Downloads the template through the gateway, preserving the auth header. */
async function downloadTemplate(): Promise<void> {
  const token = getAccessToken();
  const response = await fetch(`${config.apiBaseUrl}/imports/template`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    credentials: "include",
  });
  if (!response.ok) return;

  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "vayal-products-template.csv";
  link.click();
  // Revoke once the browser has taken the blob, or the object leaks.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
