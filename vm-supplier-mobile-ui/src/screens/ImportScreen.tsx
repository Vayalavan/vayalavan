/**
 * The CSV import wizard: pick -> preview -> confirm -> result.
 *
 * The two server calls mirror CLAUDE.md §6.5 exactly. Picking a file only
 * validates and stores a report; nothing reaches the catalogue until the
 * supplier presses Import, so a bad file costs them nothing but a re-pick.
 *
 * Two things differ from the web version, both forced by the platform:
 *   - there is no drag and drop, so the file comes from the system picker
 *     (Files, Drive, whatever the handset has);
 *   - the template cannot be "downloaded" to a folder a phone user can find,
 *     so it is fetched to the cache and handed to the share sheet, which is
 *     how you get a file onto a phone and into a spreadsheet app.
 */
import { useCallback, useState, type ReactElement } from "react";
import { View } from "react-native";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import * as DocumentPicker from "expo-document-picker";
// See the note in components/ProductImageField.tsx: on SDK 54 the network
// helpers live under /legacy, not on the File class.
import * as FileSystem from "expo-file-system/legacy";
import * as Sharing from "expo-sharing";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge, Banner } from "../components/Feedback";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import { ApiError } from "../lib/api";
import { api, getAccessToken } from "../lib/client";
import { config } from "../lib/config";
import { formatGrams, formatPaise } from "../lib/money";
import type { ImportPreview, ImportResult } from "../lib/types";
import type { ProductsStackParamList } from "../navigation/types";
import { colors, spacing } from "../theme/tokens";

type Step = "pick" | "preview" | "done";

const TEMPLATE_FILENAME = "vayal-products-template.csv";

type Props = NativeStackScreenProps<ProductsStackParamList, "Import">;

export function ImportScreen({ navigation }: Props): ReactElement {
  const [step, setStep] = useState<Step>("pick");
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [templateBusy, setTemplateBusy] = useState(false);
  const queryClient = useQueryClient();

  const upload = useMutation<ImportPreview, ApiError, { uri: string; name: string }>({
    mutationFn: ({ uri, name }) => uploadCSV(uri, name),
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

  const pickFile = useCallback(async () => {
    setError(null);
    const picked = await DocumentPicker.getDocumentAsync({
      // Android file providers are inconsistent about the MIME type they
      // report for a CSV, so accept the plain-text families too rather than
      // greying out the file the supplier is looking straight at.
      type: ["text/csv", "text/comma-separated-values", "text/plain", "*/*"],
      // Copies out of the content:// provider into the app's cache, which is
      // what makes the URI readable for an upload at all on Android.
      copyToCacheDirectory: true,
      multiple: false,
    });

    const asset = picked.assets?.[0];
    if (picked.canceled || !asset) return;

    if (!asset.name.toLowerCase().endsWith(".csv")) {
      setError(`"${asset.name}" is not a CSV file. Export your spreadsheet as CSV first.`);
      return;
    }

    upload.mutate({ uri: asset.uri, name: asset.name });
  }, [upload]);

  const shareTemplate = useCallback(async () => {
    setTemplateBusy(true);
    setError(null);
    try {
      await downloadTemplate();
    } catch {
      setError("The template could not be downloaded. Check your connection and try again.");
    } finally {
      setTemplateBusy(false);
    }
  }, []);

  return (
    <Screen>
      <PageHeading
        title="Import products from CSV"
        description="Upload a spreadsheet to add many products at once."
      />

      <Stepper step={step} />

      {error !== null && (
        <Banner tone="error" message={error} onDismiss={() => setError(null)} />
      )}

      {step === "pick" && (
        <>
          <Card>
            <View style={{ gap: spacing.md }}>
              <Text variant="heading" tone="strong">
                Choose your file
              </Text>
              <Text tone="muted">
                Up to 5 MB and 2000 rows. Nothing is saved until you confirm.
              </Text>
              <Button
                label={upload.isPending ? "Checking your file…" : "Choose a CSV file"}
                onPress={() => void pickFile()}
                loading={upload.isPending}
                block
              />
            </View>
          </Card>

          <Card>
            <View style={{ gap: spacing.sm }}>
              <Text variant="bodyStrong" tone="strong">
                Not sure about the format?
              </Text>
              <Text variant="caption" tone="muted">
                Columns: name, type, grade, description, unit_label, weight_grams,
                price_rupees, image_url. Rows sharing a name and grade become one product
                with several units.
              </Text>
              <Button
                label={templateBusy ? "Preparing…" : "Get the template"}
                onPress={() => void shareTemplate()}
                loading={templateBusy}
                variant="secondary"
                size="sm"
                accessibilityHint="Downloads the template and opens the share sheet so you can save it or open it in a spreadsheet app"
              />
            </View>
          </Card>
        </>
      )}

      {step === "preview" && preview !== null && (
        <PreviewStep
          preview={preview}
          committing={commit.isPending}
          onBack={() => {
            setPreview(null);
            setError(null);
            setStep("pick");
          }}
          onConfirm={() => commit.mutate()}
        />
      )}

      {step === "done" && result !== null && (
        <Card>
          <View style={{ gap: spacing.md }}>
            <Text variant="title" tone="strong">
              Import complete
            </Text>

            <View style={{ flexDirection: "row", gap: spacing.xl, flexWrap: "wrap" }}>
              <Stat label="Products created" value={result.products_created} />
              <Stat label="Rows imported" value={result.rows_imported} />
              <Stat label="Rows skipped" value={result.rows_skipped} />
            </View>

            <Text tone="muted">
              Imported products start as drafts so you can review them before they go on
              sale.
            </Text>

            <View style={{ gap: spacing.sm }}>
              <Button
                label="Review products"
                onPress={() => navigation.navigate("ProductList")}
                block
              />
              <Button
                label="Import another file"
                onPress={() => {
                  setStep("pick");
                  setPreview(null);
                  setResult(null);
                }}
                variant="secondary"
                block
              />
            </View>
          </View>
        </Card>
      )}
    </Screen>
  );
}

function Stepper({ step }: { step: Step }): ReactElement {
  const steps: { key: Step; label: string }[] = [
    { key: "pick", label: "1. Choose" },
    { key: "preview", label: "2. Review" },
    { key: "done", label: "3. Done" },
  ];
  const activeIndex = steps.findIndex((s) => s.key === step);

  return (
    <View style={{ flexDirection: "row", gap: spacing.sm }}>
      {steps.map((s, index) => (
        <View
          key={s.key}
          accessibilityRole="text"
          accessibilityState={{ selected: index === activeIndex }}
          style={{
            flex: 1,
            paddingVertical: spacing.sm,
            paddingHorizontal: spacing.md,
            borderRadius: 999,
            alignItems: "center",
            backgroundColor:
              index === activeIndex
                ? colors.primary[600]
                : index < activeIndex
                  ? colors.primary[100]
                  : colors.surface.sunken,
          }}
        >
          <Text
            variant="caption"
            tone={index === activeIndex ? "onPrimary" : index < activeIndex ? "strong" : "faint"}
          >
            {s.label}
          </Text>
        </View>
      ))}
    </View>
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
  const problems = preview.rows.filter((row) => row.status === "error");

  return (
    <>
      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            {preview.filename}
          </Text>
          <Text tone="muted">
            {preview.valid_rows} of {preview.total_rows} rows are ready to import
            {preview.error_rows > 0 ? `, ${preview.error_rows} have problems` : ""}.
          </Text>

          <View style={{ flexDirection: "row", gap: spacing.xl, flexWrap: "wrap" }}>
            <Stat label="Products" value={preview.products.length} />
            <Stat label="Valid" value={preview.valid_rows} />
            <Stat label="Errors" value={preview.error_rows} />
          </View>
        </View>
      </Card>

      {preview.error_rows > 0 && (
        <Banner
          tone="warning"
          message="Rows with problems are skipped. Fix them in your spreadsheet and choose the file again if you want them included."
        />
      )}

      {/* Only the failing rows are listed. The web app shows all of them in a
          table; on a phone, scrolling 2000 rows to find the four that are
          broken is not review, it is punishment. */}
      {problems.length > 0 && (
        <Card>
          <View style={{ gap: spacing.md }}>
            <Text variant="heading" tone="strong">
              Rows that need fixing
            </Text>
            {problems.map((row) => (
              <View key={row.line} style={{ gap: spacing.xs }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
                  <Badge label={`Line ${row.line}`} tone="accent" />
                  <Text variant="bodyStrong" tone="strong" style={{ flex: 1 }}>
                    {row.parsed?.name ?? "—"}
                  </Text>
                </View>
                {row.errors?.map((message) => (
                  <Text key={message} variant="caption" tone="danger">
                    • {message}
                  </Text>
                ))}
              </View>
            ))}
          </View>
        </Card>
      )}

      {preview.products.length > 0 && (
        <Card>
          <View style={{ gap: spacing.md }}>
            <Text variant="heading" tone="strong">
              Products that will be created
            </Text>
            {preview.products.map((product, index) => (
              <View
                key={`${product.name}-${product.grade}`}
                style={{
                  gap: spacing.xs,
                  ...(index > 0 && {
                    borderTopWidth: 1,
                    borderTopColor: colors.surface.border,
                    paddingTop: spacing.md,
                  }),
                }}
              >
                <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm, flexWrap: "wrap" }}>
                  <Text variant="bodyStrong" tone="strong">
                    {product.name}
                  </Text>
                  {product.grade !== "" && <Badge label={`Grade ${product.grade}`} />}
                  <Badge label={product.type} />
                </View>
                {product.units.map((unit) => (
                  <Text key={unit.weight_grams} variant="caption" tone="muted" tabular>
                    {unit.label} ({formatGrams(unit.weight_grams)}) —{" "}
                    {formatPaise(unit.price_paise)}
                  </Text>
                ))}
              </View>
            ))}
          </View>
        </Card>
      )}

      <View style={{ gap: spacing.sm }}>
        <Button
          label={
            committing
              ? "Importing…"
              : `Import ${preview.valid_rows} row${preview.valid_rows === 1 ? "" : "s"}`
          }
          onPress={onConfirm}
          disabled={!hasValid}
          loading={committing}
          block
        />
        <Button
          label="Choose a different file"
          onPress={onBack}
          disabled={committing}
          variant="secondary"
          block
        />
      </View>
    </>
  );
}

function Stat({ label, value }: { label: string; value: number }): ReactElement {
  return (
    <View style={{ gap: 2 }}>
      <Text variant="caption" tone="faint">
        {label}
      </Text>
      <Text variant="title" tone="strong" tabular>
        {value}
      </Text>
    </View>
  );
}

/**
 * Uploads the CSV as multipart/form-data.
 *
 * React Native's FormData takes a `{ uri, name, type }` descriptor in place of
 * a browser File — the native networking layer streams the file off disk from
 * that. TypeScript's DOM lib has no such shape, hence the cast; it is the
 * documented React Native contract, not a workaround.
 *
 * Goes through the shared client so the Authorization and X-Request-Id headers
 * are applied the same way as every other call. The web app hand-rolls this
 * one request with `fetch`, which is exactly why its CSV uploads are the ones
 * missing from a request-id log search.
 */
async function uploadCSV(uri: string, name: string): Promise<ImportPreview> {
  const form = new FormData();
  form.append("file", {
    uri,
    name,
    type: "text/csv",
  } as unknown as Blob);

  return api.postForm<ImportPreview>("/imports", form, {
    // A 2000-row file over rural mobile data takes longer than a JSON call.
    timeoutMs: 120_000,
  });
}

/**
 * Fetches the template and opens the share sheet.
 *
 * A phone has no "Downloads folder" a supplier can reliably reach, so writing
 * the file and walking away would leave it somewhere they never find. Handing
 * it to the share sheet lets them put it straight into Sheets, Excel, Drive or
 * WhatsApp — which is where they will actually edit it.
 */
async function downloadTemplate(): Promise<void> {
  const token = getAccessToken();

  if (FileSystem.cacheDirectory === null) {
    throw new ApiError(
      0,
      "NO_CACHE_DIRECTORY",
      "This device has nowhere to save the template.",
    );
  }

  // The endpoint is authenticated, so this cannot be a plain Linking.openURL —
  // the browser would carry no token and get a 401. downloadAsync overwrites
  // whatever is already at the path, so fetching the template twice is fine.
  const result = await FileSystem.downloadAsync(
    `${config.apiBaseUrl.replace(/\/+$/, "")}/imports/template`,
    FileSystem.cacheDirectory + TEMPLATE_FILENAME,
    { headers: token !== null ? { Authorization: `Bearer ${token}` } : {} },
  );

  // downloadAsync writes the file whatever the status code, so a 401 would
  // otherwise be "shared" to the supplier as a CSV containing an error blob.
  if (result.status < 200 || result.status >= 300) {
    throw new ApiError(
      result.status,
      "TEMPLATE_DOWNLOAD_FAILED",
      "The template could not be downloaded.",
    );
  }

  if (!(await Sharing.isAvailableAsync())) {
    throw new ApiError(
      0,
      "SHARING_UNAVAILABLE",
      "This device cannot share files. Download the template on a computer instead.",
    );
  }

  await Sharing.shareAsync(result.uri, {
    mimeType: "text/csv",
    dialogTitle: "Vayalavan product template",
    UTI: "public.comma-separated-values-text",
  });
}
