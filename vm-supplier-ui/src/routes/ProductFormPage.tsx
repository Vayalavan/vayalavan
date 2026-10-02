import { useEffect, useState, type FormEvent, type ReactElement } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { previewRupees } from "../lib/money.js";
import { MediaUpload, type MediaItem } from "../components/MediaUpload.js";
import { PRODUCT_TYPES, type Product, type ProductType } from "../lib/types.js";

/** One editable pack row. Prices stay STRINGS end to end (CLAUDE.md rule 1). */
interface PackDraft {
  /** Local-only key so React can track rows through insertion and removal. */
  key: string;
  label: string;
  /** Optional detail line: "6-8 fruit". The label stays chip-short. */
  meta: string;
  weightGrams: string;
  priceRupees: string;
}

/**
 * One editable SIZE CODE — a grade, with its own gallery and its own packs.
 *
 * The nesting is the point: 1 kg of M and 1 kg of XL are different goods at
 * different prices, photographed separately, stocked separately. A flat pack
 * list cannot say that.
 */
interface SizeCodeDraft {
  key: string;
  /** The server id, when this grade already exists. Empty for a new one. */
  id: string;
  code: string;
  meta: string;
  /**
   * Roughly what percent of the harvest this grade is, as typed. A STRING
   * because the input is a string and "" has to survive as "not said" — a
   * number field would turn an empty box into 0, which is a share no harvest
   * has.
   */
  harvestSharePct: string;
  media: MediaItem[];
  packs: PackDraft[];
}

function emptyPack(): PackDraft {
  return { key: crypto.randomUUID(), label: "", meta: "", weightGrams: "", priceRupees: "" };
}

function emptySizeCode(): SizeCodeDraft {
  return {
    key: crypto.randomUUID(),
    id: "",
    code: "",
    meta: "",
    harvestSharePct: "",
    media: [],
    packs: [emptyPack()],
  };
}

/**
 * Field-level errors returned by the server, keyed as
 * "size_codes.0.packs.1.price_rupees" — the flattened path of the tree.
 */
type FieldErrors = Record<string, string>;

export function ProductFormPage(): ReactElement {
  const { id } = useParams<{ id: string }>();
  const isEdit = Boolean(id && id !== "new");
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [name, setName] = useState("");
  const [type, setType] = useState<ProductType>("vegetable");
  const [grade, setGrade] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState("draft");
  /**
   * The PRODUCT's own gallery — the field, the packing shed, the grower —
   * shown to a customer after whichever grade's pictures they are looking at.
   * Uploaded once here instead of once per crate.
   */
  const [media, setMedia] = useState<MediaItem[]>([]);
  const [sizeCodes, setSizeCodes] = useState<SizeCodeDraft[]>([emptySizeCode()]);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);

  const existing = useQuery<Product, ApiError>({
    queryKey: ["product", id],
    queryFn: () => api.get<Product>(`/products/${id}`),
    enabled: isEdit,
  });

  // Populate the form once the product arrives.
  useEffect(() => {
    const product = existing.data;
    if (!product) return;

    setName(product.name);
    setType(product.type);
    setGrade(product.grade ?? "");
    setDescription(product.description ?? "");
    setStatus(product.status);
    setMedia(
      product.media.map((item) => ({
        kind: item.kind,
        objectKey: item.object_key,
        contentType: item.content_type ?? "",
        previewUrl: item.url,
        // Presigned URLs from the server, not object URLs we created — so
        // nothing here is ours to revoke.
        isLocal: false,
      })),
    );
    setSizeCodes(
      product.size_codes.map((sc) => ({
        key: sc.id,
        id: sc.id,
        code: sc.code,
        meta: sc.meta ?? "",
        harvestSharePct:
          sc.harvest_share_pct === null ? "" : String(sc.harvest_share_pct),
        media: sc.media.map((item) => ({
          kind: item.kind,
          objectKey: item.object_key,
          contentType: item.content_type ?? "",
          previewUrl: item.url,
          // Presigned URLs from the server, not object URLs we created — so
          // nothing here is ours to revoke.
          isLocal: false,
        })),
        packs: sc.packs.map((pack) => ({
          key: pack.id,
          label: pack.label,
          meta: pack.meta ?? "",
          weightGrams: String(pack.weight_grams),
          // Round-tripped from integer paise, never from a float.
          priceRupees: (pack.price_paise / 100).toFixed(2),
        })),
      })),
    );
  }, [existing.data]);

  const save = useMutation<Product, ApiError>({
    mutationFn: () => {
      const payload = {
        name,
        type,
        grade,
        description,
        status,
        media: media.map((item) => ({
          kind: item.kind,
          object_key: item.objectKey,
          content_type: item.contentType,
        })),
        size_codes: sizeCodes.map((sc) => ({
          code: sc.code,
          meta: sc.meta,
          // "" means the grower has not estimated their split, and null is how
          // that is said on the wire. Number("") is 0, which would be a claim
          // they never made.
          harvest_share_pct:
            sc.harvestSharePct.trim() === "" ? null : Number(sc.harvestSharePct),
          media: sc.media.map((item) => ({
            kind: item.kind,
            object_key: item.objectKey,
            content_type: item.contentType,
          })),
          packs: sc.packs.map((pack) => ({
            label: pack.label,
            meta: pack.meta,
            weight_grams: Number(pack.weightGrams),
            price_rupees: pack.priceRupees,
          })),
        })),
      };
      return isEdit
        ? api.put<Product>(`/products/${id}`, payload)
        : api.post<Product>("/products", payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      navigate("/products");
    },
    onError: (err) => {
      setFieldErrors(flattenDetails(err.details));
      setFormError(err.details ? null : err.message);
    },
  });

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    setFieldErrors({});
    setFormError(null);
    save.mutate();
  }

  function updateSizeCode(index: number, patch: Partial<SizeCodeDraft>) {
    setSizeCodes((current) =>
      current.map((sc, i) => (i === index ? { ...sc, ...patch } : sc)),
    );
  }

  function updatePack(sizeIndex: number, packIndex: number, patch: Partial<PackDraft>) {
    setSizeCodes((current) =>
      current.map((sc, i) =>
        i === sizeIndex
          ? {
              ...sc,
              packs: sc.packs.map((pack, j) =>
                j === packIndex ? { ...pack, ...patch } : pack,
              ),
            }
          : sc,
      ),
    );
  }

  /**
   * Moves one grade by `delta` places, clamped to the ends.
   *
   * Position IS the order — the payload carries no sort_order field, the
   * server assigns one from the array index, and every read orders by it. So
   * this decides the grade selector's order, which grade the shop opens on,
   * and therefore which cover becomes the product's thumbnail.
   *
   * Arrows rather than a drag handle, matching MediaUpload and the supplier
   * app: a grower has three or four grades, two taps beat a gesture, and this
   * way there is one mechanism to keep working and it reaches the keyboard.
   */
  function moveSizeCode(index: number, delta: number) {
    setSizeCodes((current) => {
      const target = index + delta;
      if (target < 0 || target >= current.length) return current;
      const next = [...current];
      const [item] = next.splice(index, 1);
      next.splice(target, 0, item!);
      return next;
    });
  }

  if (isEdit && existing.isPending) return <Card>Loading…</Card>;
  if (isEdit && existing.isError) {
    return (
      <Card>
        <p role="alert" className="text-accent-800">
          {existing.error.message}
        </p>
      </Card>
    );
  }

  return (
    <>
      <PageHeading
        title={isEdit ? "Edit product" : "Add product"}
        description="Size codes are the grades you sell — each with its own photos, pack sizes and prices. Saving replaces the whole list."
      />

      <form onSubmit={onSubmit} noValidate className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <label htmlFor="name" className="block text-sm font-medium text-primary-900">
                  Name
                </label>
                <input
                  id="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2"
                  placeholder="Tomato"
                />
                <FieldError message={fieldErrors["name"]} />
              </div>

              <div>
                <label htmlFor="type" className="block text-sm font-medium text-primary-900">
                  Type
                </label>
                <select
                  id="type"
                  value={type}
                  onChange={(e) => setType(e.target.value as ProductType)}
                  className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2"
                >
                  {PRODUCT_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {t[0]!.toUpperCase() + t.slice(1)}
                    </option>
                  ))}
                </select>
                <FieldError message={fieldErrors["type"]} />
              </div>

              <div>
                <label htmlFor="grade" className="block text-sm font-medium text-primary-900">
                  Grade <span className="text-primary-900/50">(optional)</span>
                </label>
                <input
                  id="grade"
                  value={grade}
                  onChange={(e) => setGrade(e.target.value)}
                  className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2"
                  placeholder="A"
                />
                <FieldError message={fieldErrors["grade"]} />
              </div>

              <div className="sm:col-span-2">
                <label htmlFor="description" className="block text-sm font-medium text-primary-900">
                  Description <span className="text-primary-900/50">(optional)</span>
                </label>
                <textarea
                  id="description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  rows={3}
                  className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2"
                />
              </div>
            </div>
          </Card>

          {/* The product's own gallery. Above the grades in the form because
              it is about the whole listing; BELOW each grade's pictures on the
              shop page, because that is where a customer wants the fruit
              first and the farm after. */}
          <Card>
            <h2 className="mb-1 font-semibold text-primary-900">
              Photos and videos for all sizes
            </h2>
            <p className="mb-3 text-sm text-primary-900/60">
              Your field, your packing, you. These are shown to customers after
              the pictures of whichever size they are looking at, so you only
              upload them once instead of adding them to every size code.
            </p>

            {typeof fieldErrors["media"] === "string" && (
              <p role="alert" className="mb-2 text-sm text-accent-800">
                {fieldErrors["media"]}
              </p>
            )}

            <MediaUpload
              value={media}
              onChange={setMedia}
              // The card's own heading says what these are; the component
              // would otherwise print a second one.
              label=""
              coverHint="These come after each size's own pictures. The first photo is a size's cover only when that size has no picture of its own."
              coverBadge={false}
            />
          </Card>

          <Card>
            <div className="mb-3 flex items-center justify-between">
              <h2 className="font-semibold text-primary-900">Size codes</h2>
              <button
                type="button"
                onClick={() => setSizeCodes((current) => [...current, emptySizeCode()])}
                className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800 hover:bg-primary-50"
              >
                Add size code
              </button>
            </div>

            <p className="mb-3 text-sm text-primary-900/60">
              A size code is a grade you sell — M, L, XL. Each has its own photos,
              its own pack sizes and prices, and its own daily stock, because the
              crates are separate. If you don&rsquo;t grade your produce, keep the
              one size code. Use the arrows to change the order customers see them
              in — the first is the one the shop opens on.
            </p>

            <p className="mb-3 text-sm text-primary-900/60">
              <span className="font-medium text-primary-900/80">Share of harvest</span> is
              optional: roughly how much of this crop comes off as that grade — say
              55% M, 30% L, 12% XL, 3% XL2. The shop uses it to show customers which
              grades are scarce, so the pick of your field does not look like the bulk
              of it. Leave it blank if you would rather not estimate, and it is never
              shown as a price or a stock figure.
            </p>

            {typeof fieldErrors["size_codes"] === "string" && (
              <p role="alert" className="mb-2 text-sm text-accent-800">
                {fieldErrors["size_codes"]}
              </p>
            )}

            <div className="space-y-4">
              {sizeCodes.map((sizeCode, sizeIndex) => (
                <div
                  key={sizeCode.key}
                  className="rounded-card border border-surface-border p-3"
                >
                  {/* Position is the order: the first grade is the one the
                      shop opens on, and its cover is the product's thumbnail. */}
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-medium text-primary-900/70">
                        Size code {sizeIndex + 1}
                      </span>
                      {sizeIndex === 0 && (
                        <span className="rounded-badge bg-primary-700 px-1.5 py-0.5 text-[10px] font-medium text-surface">
                          Shown first
                        </span>
                      )}
                    </div>
                    <div className="flex gap-0.5">
                      <button
                        type="button"
                        onClick={() => moveSizeCode(sizeIndex, -1)}
                        disabled={sizeIndex === 0}
                        aria-label={`Move size code ${sizeIndex + 1} earlier`}
                        className="rounded px-1.5 py-0.5 text-sm text-primary-700 disabled:opacity-30"
                      >
                        ↑
                      </button>
                      <button
                        type="button"
                        onClick={() => moveSizeCode(sizeIndex, 1)}
                        disabled={sizeIndex === sizeCodes.length - 1}
                        aria-label={`Move size code ${sizeIndex + 1} later`}
                        className="rounded px-1.5 py-0.5 text-sm text-primary-700 disabled:opacity-30"
                      >
                        ↓
                      </button>
                    </div>
                  </div>

                  <div className="grid gap-3 sm:grid-cols-[1fr_2fr_auto_auto]">
                    <div>
                      <label className="block text-xs font-medium text-primary-900/70">
                        Code
                      </label>
                      <input
                        value={sizeCode.code}
                        onChange={(e) => updateSizeCode(sizeIndex, { code: e.target.value })}
                        placeholder="M2"
                        className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                      />
                      <FieldError message={fieldErrors[`size_codes.${sizeIndex}.code`]} />
                    </div>

                    <div>
                      <label className="block text-xs font-medium text-primary-900/70">
                        Detail <span className="text-primary-900/50">(optional)</span>
                      </label>
                      <input
                        value={sizeCode.meta}
                        onChange={(e) => updateSizeCode(sizeIndex, { meta: e.target.value })}
                        placeholder="150 g - 200 g"
                        className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                      />
                      <FieldError message={fieldErrors[`size_codes.${sizeIndex}.meta`]} />
                    </div>

                    {/* What share of the harvest this grade is. Optional, and
                        the storefront is the only thing that reads it — it
                        decides how this grade's chip is drawn, gold through
                        silver. No price or stock depends on it. */}
                    <div>
                      <label className="block text-xs font-medium text-primary-900/70">
                        Share of harvest
                      </label>
                      <div className="mt-1 flex items-center gap-1">
                        <input
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={100}
                          value={sizeCode.harvestSharePct}
                          onChange={(e) =>
                            updateSizeCode(sizeIndex, { harvestSharePct: e.target.value })
                          }
                          placeholder="—"
                          aria-label={`Share of harvest for size code ${sizeIndex + 1}, percent`}
                          className="w-20 rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                        />
                        <span className="text-sm text-primary-900/60">%</span>
                      </div>
                      <FieldError
                        message={fieldErrors[`size_codes.${sizeIndex}.harvest_share_pct`]}
                      />
                    </div>

                    <div className="flex items-end">
                      <button
                        type="button"
                        onClick={() =>
                          setSizeCodes((current) =>
                            // Never leave zero grades: a product with none
                            // cannot be sold, and the server rejects it.
                            current.length === 1
                              ? [emptySizeCode()]
                              : current.filter((_, i) => i !== sizeIndex),
                          )
                        }
                        aria-label={`Remove size code ${sizeIndex + 1}`}
                        className="mb-1 rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70 hover:bg-surface-raised"
                      >
                        Remove
                      </button>
                    </div>
                  </div>

                  {/* This grade's own gallery. Switching size code on the shop
                      page swaps these pictures, so they must be of THIS grade. */}
                  <div className="mt-3 border-t border-surface-border pt-3">
                    <MediaUpload
                      value={sizeCode.media}
                      onChange={(items) => updateSizeCode(sizeIndex, { media: items })}
                    />
                  </div>

                  <div className="mt-3 border-t border-surface-border pt-3">
                    <div className="mb-2 flex items-center justify-between">
                      <h3 className="text-sm font-medium text-primary-900">Pack sizes</h3>
                      <button
                        type="button"
                        onClick={() =>
                          updateSizeCode(sizeIndex, {
                            packs: [...sizeCode.packs, emptyPack()],
                          })
                        }
                        className="rounded-card border border-surface-border px-2 py-1 text-xs font-medium text-primary-800 hover:bg-primary-50"
                      >
                        Add pack
                      </button>
                    </div>

                    {typeof fieldErrors[`size_codes.${sizeIndex}.packs`] === "string" && (
                      <p role="alert" className="mb-2 text-sm text-accent-800">
                        {fieldErrors[`size_codes.${sizeIndex}.packs`]}
                      </p>
                    )}

                    <div className="space-y-3">
                      {sizeCode.packs.map((pack, packIndex) => {
                        // Live rupee preview: shows what the price will
                        // actually be stored as, in Indian grouping, while the
                        // supplier types.
                        const preview = previewRupees(pack.priceRupees);
                        const path = `size_codes.${sizeIndex}.packs.${packIndex}`;

                        return (
                          <div key={pack.key} className="rounded-card bg-surface-sunken p-3">
                            <div className="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto]">
                            <div>
                              <label className="block text-xs font-medium text-primary-900/70">
                                Label
                              </label>
                              <input
                                value={pack.label}
                                onChange={(e) =>
                                  updatePack(sizeIndex, packIndex, { label: e.target.value })
                                }
                                placeholder="1 kg box"
                                className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                              />
                              <FieldError message={fieldErrors[`${path}.label`]} />
                            </div>

                            <div>
                              <label className="block text-xs font-medium text-primary-900/70">
                                Weight (grams)
                              </label>
                              <input
                                type="number"
                                inputMode="numeric"
                                min={1}
                                value={pack.weightGrams}
                                onChange={(e) =>
                                  updatePack(sizeIndex, packIndex, {
                                    weightGrams: e.target.value,
                                  })
                                }
                                placeholder="1000"
                                className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                              />
                              <FieldError message={fieldErrors[`${path}.weight_grams`]} />
                            </div>

                            <div>
                              <label className="block text-xs font-medium text-primary-900/70">
                                Price (₹)
                              </label>
                              {/* type=text, not number: a number input would
                                  let the browser localise and round the value,
                                  and money must survive exactly as typed. */}
                              <input
                                type="text"
                                inputMode="decimal"
                                value={pack.priceRupees}
                                onChange={(e) =>
                                  updatePack(sizeIndex, packIndex, {
                                    priceRupees: e.target.value,
                                  })
                                }
                                placeholder="1000.00"
                                className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                              />
                              <p className="mt-1 text-xs text-primary-900/60">
                                {preview ? `Saves as ${preview}` : " "}
                              </p>
                              <FieldError message={fieldErrors[`${path}.price_rupees`]} />
                            </div>

                            <div className="flex items-end">
                              <button
                                type="button"
                                onClick={() =>
                                  updateSizeCode(sizeIndex, {
                                    // Never leave zero packs: a grade with none
                                    // cannot be sold, and the server rejects it.
                                    packs:
                                      sizeCode.packs.length === 1
                                        ? [emptyPack()]
                                        : sizeCode.packs.filter((_, i) => i !== packIndex),
                                  })
                                }
                                aria-label={`Remove pack ${packIndex + 1} of size code ${sizeIndex + 1}`}
                                className="mb-6 rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70 hover:bg-surface-raised"
                              >
                                Remove
                              </button>
                            </div>
                            </div>

                            {/* On its own line, and full width: this is the
                                sentence that would not fit in the label —
                                "6-8 fruit", "ventilated carton" — and giving
                                it a narrow column would just move the problem.
                                The customer sees it under the pack's name on
                                the product page. */}
                            <div className="mt-3">
                              <label className="block text-xs font-medium text-primary-900/70">
                                Pack detail <span className="text-primary-900/50">(optional)</span>
                              </label>
                              <input
                                value={pack.meta}
                                onChange={(e) =>
                                  updatePack(sizeIndex, packIndex, { meta: e.target.value })
                                }
                                placeholder="6-8 fruit, ventilated carton"
                                className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-2 py-1.5 text-sm"
                              />
                              <FieldError message={fieldErrors[`${path}.meta`]} />
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </Card>
        </div>

        <div className="space-y-4">
          <Card>
            <label htmlFor="status" className="block text-sm font-medium text-primary-900">
              Status
            </label>
            <select
              id="status"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2"
            >
              <option value="draft">Draft — not visible to customers</option>
              <option value="active">Active — on sale</option>
              <option value="archived">Archived</option>
            </select>

            {formError && (
              <p role="alert" className="mt-3 text-sm text-accent-800">
                {formError}
              </p>
            )}

            <div className="mt-4 flex gap-2">
              <button
                type="submit"
                disabled={save.isPending}
                className="flex-1 rounded-card bg-primary-600 px-4 py-2.5 font-medium text-white hover:bg-primary-700 disabled:opacity-60"
              >
                {save.isPending ? "Saving…" : isEdit ? "Save changes" : "Create product"}
              </button>
              <button
                type="button"
                onClick={() => navigate("/products")}
                className="rounded-card border border-surface-border px-4 py-2.5 font-medium text-primary-800"
              >
                Cancel
              </button>
            </div>
          </Card>
        </div>
      </form>
    </>
  );
}

function FieldError({ message }: { message: string | undefined }): ReactElement | null {
  if (!message) return null;
  return (
    <p role="alert" className="mt-1 text-xs text-accent-800">
      {message}
    </p>
  );
}

/**
 * Flattens the server's nested validation details into dotted keys.
 *
 * The API reports unit problems as {units: {"0": {price_rupees: "..."}}};
 * the form looks them up as "units.0.price_rupees" so each input can show its
 * own message.
 */
function flattenDetails(details: Record<string, unknown> | undefined): FieldErrors {
  const flat: FieldErrors = {};
  if (!details) return flat;

  for (const [key, value] of Object.entries(details)) {
    if (typeof value === "string") {
      flat[key] = value;
      continue;
    }
    if (value && typeof value === "object") {
      for (const [index, problems] of Object.entries(value as Record<string, unknown>)) {
        if (problems && typeof problems === "object") {
          for (const [field, message] of Object.entries(problems as Record<string, unknown>)) {
            if (typeof message === "string") flat[`${key}.${index}.${field}`] = message;
          }
        }
      }
    }
  }
  return flat;
}
