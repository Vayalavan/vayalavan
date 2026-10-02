import { useEffect, useLayoutEffect, useState, type ReactElement } from "react";
import { View } from "react-native";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Banner, ErrorState, Loading } from "../components/Feedback";
import { ChoiceField, TextField } from "../components/Field";
import { ProductMediaField, type MediaItem } from "../components/ProductMediaField";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { previewRupees } from "../lib/money";
import { PRODUCT_TYPES, type Product, type ProductType } from "../lib/types";
import type { ProductsStackParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";

/** One editable unit row. Prices stay STRINGS end to end (CLAUDE.md rule 1). */
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
 * different prices, photographed separately, stocked separately.
 */
interface SizeCodeDraft {
  key: string;
  code: string;
  meta: string;
  /**
   * Roughly what percent of the harvest this grade is, as typed. A STRING
   * because the input is a string and "" has to survive as "not said" — a
   * number would turn an empty box into 0, which is a share no harvest has.
   */
  harvestSharePct: string;
  media: MediaItem[];
  packs: PackDraft[];
}

/**
 * A key for a freshly added row.
 *
 * `crypto.randomUUID` does not exist in Hermes, and this only has to be unique
 * within one form's list — a counter plus the clock is more than enough.
 */
let rowKeySeed = 0;
function nextKey(): string {
  rowKeySeed += 1;
  return `new-${Date.now().toString(36)}-${rowKeySeed}`;
}

function emptyPack(): PackDraft {
  return { key: nextKey(), label: "", meta: "", weightGrams: "", priceRupees: "" };
}

function emptySizeCode(): SizeCodeDraft {
  return {
    key: nextKey(),
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

const TYPE_OPTIONS = PRODUCT_TYPES.map((t) => ({
  value: t,
  label: t[0]!.toUpperCase() + t.slice(1),
}));

const STATUS_OPTIONS = [
  { value: "draft", label: "Draft", description: "Not visible to customers" },
  { value: "active", label: "Active", description: "On sale" },
  { value: "archived", label: "Archived", description: "Hidden and not listed" },
] as const;

type Props = NativeStackScreenProps<ProductsStackParamList, "ProductForm">;

export function ProductFormScreen({ navigation, route }: Props): ReactElement {
  const productId = route.params?.productId;
  const isEdit = productId !== undefined;
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

  useLayoutEffect(() => {
    navigation.setOptions({ title: isEdit ? "Edit product" : "Add product" });
  }, [navigation, isEdit]);

  const existing = useQuery<Product, ApiError>({
    queryKey: ["product", productId],
    queryFn: () => api.get<Product>(`/products/${productId}`),
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
      })),
    );
    setSizeCodes(
      product.size_codes.map((sc) => ({
        key: sc.id,
        code: sc.code,
        meta: sc.meta ?? "",
        harvestSharePct:
          sc.harvest_share_pct === null ? "" : String(sc.harvest_share_pct),
        media: sc.media.map((item) => ({
          kind: item.kind,
          objectKey: item.object_key,
          contentType: item.content_type ?? "",
          previewUrl: item.url,
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
            // Sent as the STRING the supplier typed. The server parses it to
            // paise with the same exact-decimal rules money.ts uses locally,
            // so the two can never disagree by a paise.
            price_rupees: pack.priceRupees,
          })),
        })),
      };
      return isEdit
        ? api.put<Product>(`/products/${productId}`, payload)
        : api.post<Product>("/products", payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      // The availability sheet lists active products, so a new one has to show
      // up there too — otherwise a supplier adds produce and cannot declare it
      // until the cache happens to expire.
      void queryClient.invalidateQueries({ queryKey: ["availability"] });
      navigation.goBack();
    },
    onError: (err) => {
      setFieldErrors(flattenDetails(err.details));
      setFormError(err.details ? null : err.message);
    },
  });

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
   * Arrows rather than a long-press drag: this list sits inside a scrolling
   * form of text inputs, where a drag gesture has to be stolen from the scroll
   * view, and a supplier doing this one-handed in a field is the case that
   * matters. The web form has the same two arrows, for the same reasons
   * MediaUpload does.
   */
  function moveSizeCode(index: number, delta: number) {
    setSizeCodes((current) => {
      const target = index + delta;
      if (target < 0 || target >= current.length) return current;
      const next = [...current];
      const [item] = next.splice(index, 1);
      if (item === undefined) return current;
      next.splice(target, 0, item);
      return next;
    });
  }

  if (isEdit && existing.isPending) return <Loading />;
  if (isEdit && existing.isError) {
    return (
      <Screen>
        <ErrorState error={existing.error} onRetry={() => void existing.refetch()} />
      </Screen>
    );
  }

  return (
    <Screen
      footer={
        <View style={{ flexDirection: "row", gap: spacing.md }}>
          <Button
            label="Cancel"
            onPress={() => navigation.goBack()}
            variant="secondary"
            disabled={save.isPending}
          />
          <Button
            label={save.isPending ? "Saving…" : isEdit ? "Save changes" : "Create product"}
            onPress={() => {
              setFieldErrors({});
              setFormError(null);
              save.mutate();
            }}
            loading={save.isPending}
            disabled={name.trim() === ""}
            style={{ flex: 1 }}
          />
        </View>
      }
    >
      <PageHeading
        title={isEdit ? "Edit product" : "Add product"}
        description="Units are the pack sizes customers buy. Saving replaces the whole unit list."
      />

      {formError !== null && <Banner tone="error" message={formError} />}

      <Card>
        <View style={{ gap: spacing.lg }}>
          <TextField
            label="Name"
            value={name}
            onChangeText={setName}
            placeholder="Tomato"
            autoCapitalize="words"
            error={fieldErrors["name"]}
          />

          <ChoiceField
            label="Type"
            value={type}
            options={TYPE_OPTIONS}
            onChange={setType}
            error={fieldErrors["type"]}
          />

          <TextField
            label="Grade"
            optional
            value={grade}
            onChangeText={setGrade}
            placeholder="A"
            autoCapitalize="characters"
            error={fieldErrors["grade"]}
          />

          <TextField
            label="Description"
            optional
            value={description}
            onChangeText={setDescription}
            multiline
          />
        </View>
      </Card>

      {/* The product's own gallery. Above the grades in the form because it is
          about the whole listing; BELOW each grade's pictures on the shop
          page, because that is where a customer wants the fruit first and the
          farm after. */}
      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            Photos and videos for all sizes
          </Text>
          <Text variant="caption" tone="muted">
            Your field, your packing, you. Customers see these after the
            pictures of whichever size they are looking at, so you upload them
            once instead of adding them to every size code.
          </Text>

          {typeof fieldErrors["media"] === "string" && (
            <Text variant="caption" tone="danger" accessibilityRole="alert">
              {fieldErrors["media"]}
            </Text>
          )}

          <ProductMediaField
            value={media}
            onChange={setMedia}
            // The card's own heading says what these are; the component would
            // otherwise print a second one.
            label=""
            coverHint="They come after each size's own pictures, and the first photo is a size's cover only when that size has none of its own."
            coverBadge={false}
          />
        </View>
      </Card>

      <Card>
        <View style={{ gap: spacing.lg }}>
          <View
            style={{
              flexDirection: "row",
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <Text variant="heading" tone="strong">
              Size codes
            </Text>
            <Button
              label="Add size code"
              onPress={() => setSizeCodes((current) => [...current, emptySizeCode()])}
              variant="secondary"
              size="sm"
            />
          </View>

          <Text variant="caption" tone="faint">
            A size code is a grade you sell — M, L, XL. Each has its own photos,
            pack sizes and prices, and its own daily stock, because the crates are
            separate. If you don&rsquo;t grade your produce, keep the one size code.
            Use the arrows to change the order customers see them in — the first
            is the one the shop opens on.
          </Text>

          {typeof fieldErrors["size_codes"] === "string" && (
            <Text variant="caption" tone="danger" accessibilityRole="alert">
              {fieldErrors["size_codes"]}
            </Text>
          )}

          {sizeCodes.map((sizeCode, sizeIndex) => (
            <View
              key={sizeCode.key}
              style={{
                gap: spacing.md,
                // A rule between grades: with a gallery and several packs
                // each, an unbroken run is hard to tell apart on a phone.
                ...(sizeIndex > 0 && {
                  borderTopWidth: 1,
                  borderTopColor: colors.surface.border,
                  paddingTop: spacing.lg,
                }),
              }}
            >
              {/* Position is the order: the first grade is the one the shop
                  opens on, and its cover is the product's thumbnail. */}
              <View
                style={{
                  flexDirection: "row",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: spacing.sm,
                }}
              >
                <View
                  style={{
                    flexDirection: "row",
                    alignItems: "center",
                    gap: spacing.sm,
                    flexShrink: 1,
                  }}
                >
                  <Text variant="label" tone="strong">
                    Size code {sizeIndex + 1}
                  </Text>
                  {sizeIndex === 0 && (
                    <View
                      style={{
                        backgroundColor: colors.primary[700],
                        borderRadius: radius.pill,
                        paddingHorizontal: spacing.sm,
                        paddingVertical: 2,
                      }}
                    >
                      <Text variant="caption" tone="onPrimary">
                        Shown first
                      </Text>
                    </View>
                  )}
                </View>

                <View style={{ flexDirection: "row", gap: spacing.sm }}>
                  <Button
                    label="↑"
                    onPress={() => moveSizeCode(sizeIndex, -1)}
                    variant="ghost"
                    size="sm"
                    disabled={sizeIndex === 0}
                    accessibilityHint={`Moves size code ${sizeIndex + 1} earlier, so customers see it sooner`}
                  />
                  <Button
                    label="↓"
                    onPress={() => moveSizeCode(sizeIndex, 1)}
                    variant="ghost"
                    size="sm"
                    disabled={sizeIndex === sizeCodes.length - 1}
                    accessibilityHint={`Moves size code ${sizeIndex + 1} later, so customers see it after the others`}
                  />
                </View>
              </View>

              <TextField
                label="Code"
                value={sizeCode.code}
                onChangeText={(value) => updateSizeCode(sizeIndex, { code: value })}
                placeholder="M2"
                autoCapitalize="characters"
                error={fieldErrors[`size_codes.${sizeIndex}.code`]}
              />

              <TextField
                label="Detail (optional)"
                value={sizeCode.meta}
                onChangeText={(value) => updateSizeCode(sizeIndex, { meta: value })}
                placeholder="150 g - 200 g"
                error={fieldErrors[`size_codes.${sizeIndex}.meta`]}
              />

              {/* What share of the harvest this grade is. The shop uses it to
                  show customers which grades are scarce; nothing prices or
                  reserves against it. */}
              <TextField
                label="Share of harvest"
                value={sizeCode.harvestSharePct}
                onChangeText={(value) =>
                  updateSizeCode(sizeIndex, { harvestSharePct: value })
                }
                placeholder="12"
                keyboardType="number-pad"
                alignRight
                suffix="%"
                optional
                hint="Roughly how much of this crop comes off as this grade. The shop shows scarce grades differently, so the pick of your field does not look like the bulk of it."
                error={fieldErrors[`size_codes.${sizeIndex}.harvest_share_pct`]}
              />

              {/* This grade's own gallery. Switching size code on the shop
                  page swaps these pictures, so they must be of THIS grade. */}
              <ProductMediaField
                value={sizeCode.media}
                onChange={(items) => updateSizeCode(sizeIndex, { media: items })}
              />

              <View
                style={{
                  flexDirection: "row",
                  alignItems: "center",
                  justifyContent: "space-between",
                }}
              >
                <Text variant="label" tone="strong">
                  Pack sizes
                </Text>
                <Button
                  label="Add pack"
                  onPress={() =>
                    updateSizeCode(sizeIndex, {
                      packs: [...sizeCode.packs, emptyPack()],
                    })
                  }
                  variant="secondary"
                  size="sm"
                />
              </View>

              {typeof fieldErrors[`size_codes.${sizeIndex}.packs`] === "string" && (
                <Text variant="caption" tone="danger" accessibilityRole="alert">
                  {fieldErrors[`size_codes.${sizeIndex}.packs`]}
                </Text>
              )}

              {sizeCode.packs.map((pack, packIndex) => {
                // Live rupee preview: shows what the price will actually be
                // stored as, in Indian grouping, while the supplier types.
                const preview = previewRupees(pack.priceRupees);
                const path = `size_codes.${sizeIndex}.packs.${packIndex}`;

                return (
                  <View key={pack.key} style={{ gap: spacing.md }}>
                    <TextField
                      label={`Pack ${packIndex + 1} label`}
                      value={pack.label}
                      onChangeText={(value) =>
                        updatePack(sizeIndex, packIndex, { label: value })
                      }
                      placeholder="1 kg box"
                      error={fieldErrors[`${path}.label`]}
                    />

                    {/* The sentence that would not fit in the label —
                        "6-8 fruit", "ventilated carton". The customer sees it
                        under the pack's name on the product screen. */}
                    <TextField
                      label="Pack detail (optional)"
                      value={pack.meta}
                      onChangeText={(value) =>
                        updatePack(sizeIndex, packIndex, { meta: value })
                      }
                      placeholder="6-8 fruit, ventilated carton"
                      error={fieldErrors[`${path}.meta`]}
                    />

                    <TextField
                      label="Weight"
                      value={pack.weightGrams}
                      onChangeText={(value) =>
                        updatePack(sizeIndex, packIndex, { weightGrams: value })
                      }
                      placeholder="1000"
                      keyboardType="number-pad"
                      alignRight
                      suffix="g"
                      error={fieldErrors[`${path}.weight_grams`]}
                    />

                    <TextField
                      label="Price for the whole pack"
                      value={pack.priceRupees}
                      onChangeText={(value) =>
                        updatePack(sizeIndex, packIndex, { priceRupees: value })
                      }
                      placeholder="1000.00"
                      // decimal-pad, never a numeric keyboard with locale help:
                      // money must survive exactly as typed.
                      keyboardType="decimal-pad"
                      alignRight
                      suffix="₹"
                      hint={preview !== null ? `Saves as ${preview}` : undefined}
                      error={fieldErrors[`${path}.price_rupees`]}
                    />

                    <Button
                      label={`Remove pack ${packIndex + 1}`}
                      onPress={() =>
                        updateSizeCode(sizeIndex, {
                          // Never leave zero packs: a grade with none cannot
                          // be sold, and the server rejects it.
                          packs:
                            sizeCode.packs.length === 1
                              ? [emptyPack()]
                              : sizeCode.packs.filter((_, i) => i !== packIndex),
                        })
                      }
                      variant="ghost"
                      size="sm"
                    />
                  </View>
                );
              })}

              <Button
                label={`Remove size code ${sizeIndex + 1}`}
                onPress={() =>
                  setSizeCodes((current) =>
                    // Never leave zero grades: a product with none cannot be
                    // sold, and the server rejects it.
                    current.length === 1
                      ? [emptySizeCode()]
                      : current.filter((_, i) => i !== sizeIndex),
                  )
                }
                variant="ghost"
                size="sm"
              />
            </View>
          ))}
        </View>
      </Card>

      <Card>
        <ChoiceField
          label="Status"
          value={status}
          options={STATUS_OPTIONS}
          onChange={setStatus}
          error={fieldErrors["status"]}
        />
      </Card>
    </Screen>
  );
}

/**
 * Flattens the server's nested validation details into dotted keys.
 *
 * The API reports unit problems as {units: {"0": {price_rupees: "..."}}}; the
 * form looks them up as "units.0.price_rupees" so each input shows its own
 * message instead of one generic banner the supplier has to decode.
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
