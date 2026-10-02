/**
 * Product gallery: photos and videos, from the camera or the library,
 * uploaded straight to object storage with presigned PUTs (CLAUDE.md §6.6).
 *
 * The bytes never pass through the gateway or the API. What comes back to the
 * form is the object KEY, which is what gets stored on the product — URLs are
 * minted fresh on every read, so changing bucket or host never breaks a row.
 *
 * The one thing this does that the web version does not, and must: it
 * downscales photos before uploading. A photo straight off a modern phone
 * camera is 8-12 MB, comfortably over the 5 MB server limit — a supplier
 * standing in a field would tap "Take photo" and be told their own camera's
 * output is too big, with no way to fix it. Resizing to 1600px and re-encoding
 * as JPEG lands a produce photo around 300-600 KB, which also matters on the
 * mobile data these uploads actually happen over.
 *
 * Video is NOT re-encoded — nothing in the Expo Go module set can transcode,
 * and a development build carrying FFmpeg is not a trade worth making for
 * this. Instead recording is capped at MAX_VIDEO_SECONDS at medium quality,
 * which keeps a clip inside the server's 50 MB limit; a longer file picked
 * from the library is rejected with its size named, which is the only
 * actionable thing to say about it.
 */
import { useCallback, useEffect, useState, type ReactElement } from "react";
import { Alert, Image, Pressable, ScrollView, View } from "react-native";
// `expo-file-system/legacy`, not the package root. On SDK 54 the root exports
// only the File/Directory/Paths classes — the network tasks (upload with
// progress, download with headers) live under /legacy and were not folded into
// the class API until SDK 57. "legacy" here means "the stable API for this
// SDK", not "deprecated in a way that matters".
import * as FileSystem from "expo-file-system/legacy";
import * as ImageManipulator from "expo-image-manipulator";
import * as ImagePicker from "expo-image-picker";
import { useVideoPlayer, VideoView } from "expo-video";

import { ApiError } from "../lib/api";
import { api } from "../lib/client";
import type { MediaKind, PresignResponse } from "../lib/types";
import { colors, radius, spacing } from "../theme/tokens";
import { Button } from "./Button";
import { Text } from "./Text";

/** Must match MAX_IMAGE_BYTES / MAX_VIDEO_BYTES / MAX_MEDIA_PER_PRODUCT. */
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const MAX_VIDEO_BYTES = 50 * 1024 * 1024;
const MAX_ITEMS = 8;

/**
 * The longest edge we upload.
 *
 * Product images are shown in a card thumbnail and, at most, a full-width
 * detail view on a phone. 1600px is generous for both and leaves room for a
 * future desktop layout without asking suppliers to re-upload.
 */
const MAX_EDGE = 1600;

/** JPEG quality. 0.8 is the point where produce still looks fresh. */
const JPEG_QUALITY = 0.8;

/**
 * Recording cap. Thirty seconds of medium-quality phone video is roughly
 * 15-35 MB, which clears the 50 MB server limit with room for a slow encoder,
 * and it is longer than anyone needs to show a crate of tomatoes.
 */
const MAX_VIDEO_SECONDS = 30;

/** One gallery item as the form holds it. */
export interface MediaItem {
  kind: MediaKind;
  objectKey: string;
  contentType: string;
  /** A presigned GET from the server, or a local file URI just uploaded. */
  previewUrl: string | null;
}

export interface ProductMediaFieldProps {
  value: MediaItem[];
  onChange: (items: MediaItem[]) => void;
  /**
   * The heading above the gallery. Defaulted rather than required, because
   * every caller but one is a size code's own gallery — the exception is the
   * product-level bucket, where the card already says what these pictures are
   * and a second "Photos and videos" would be two headings for one control.
   * Pass "" to render none.
   */
  label?: string;
  /**
   * What the first photo means here. Inside a grade it is that grade's cover;
   * in the product-level bucket it is a cover only for a grade with no
   * picture of its own, so the caller says so rather than this component
   * asserting something untrue.
   */
  coverHint?: string;
  /** Whether the first image wears a "Cover" badge. See coverHint. */
  coverBadge?: boolean;
}

/** Content types the server signs. Must match allowedMediaTypes. */
const VIDEO_TYPES: Record<string, string> = {
  mp4: "video/mp4",
  mov: "video/quicktime",
  webm: "video/webm",
};

/**
 * Works out what to declare for a picked video.
 *
 * The picker reports a mimeType on both platforms for library picks but not
 * always for a fresh recording, so the file extension is the fallback — and
 * anything unrecognised is declared MP4, which is what both platforms record
 * by default and what the server will refuse if it is wrong.
 */
function videoContentType(uri: string, reported: string | undefined): string {
  if (reported !== undefined && Object.values(VIDEO_TYPES).includes(reported)) {
    return reported;
  }
  const extension = uri.split("?")[0]?.split(".").pop()?.toLowerCase() ?? "";
  return VIDEO_TYPES[extension] ?? "video/mp4";
}

function megabytes(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function ProductMediaField({
  value,
  onChange,
  label = "Photos and videos",
  coverHint = "The first photo is the cover customers see.",
  coverBadge = true,
}: ProductMediaFieldProps): ReactElement {
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [active, setActive] = useState(0);

  const uploading = progress !== null;
  const current = value[Math.min(active, value.length - 1)];
  const coverIndex = value.findIndex((item) => item.kind === "image");

  // Keep the selection inside the list when an item is removed.
  useEffect(() => {
    if (active > 0 && active >= value.length) setActive(value.length - 1);
  }, [active, value.length]);

  // The hook must run on every render, so the player exists whether or not the
  // selected item is a video. A null source is a player with nothing loaded.
  const player = useVideoPlayer(
    current?.kind === "video" ? (current.previewUrl ?? null) : null,
    (instance) => {
      instance.loop = false;
      instance.muted = false;
    },
  );

  /**
   * Uploads one already-prepared file and appends it to the gallery.
   *
   * Returns nothing: every failure is reported through `error`, because a
   * supplier in a field needs the reason on screen, not in a rejected promise.
   */
  const upload = useCallback(
    async (uri: string, kind: MediaKind, contentType: string, filename: string) => {
      setError(null);
      setProgress(0);

      try {
        const info = await FileSystem.getInfoAsync(uri);
        const size = info.exists ? info.size : 0;
        const limit = kind === "video" ? MAX_VIDEO_BYTES : MAX_IMAGE_BYTES;

        if (size > limit) {
          throw new ApiError(
            413,
            "MEDIA_TOO_LARGE",
            kind === "video"
              ? `That video is ${megabytes(size)}. The limit is ${megabytes(limit)} — try a shorter clip.`
              : `That photo is still ${megabytes(size)} after resizing. The limit is ${megabytes(limit)}.`,
          );
        }

        // Ask the server to sign an upload. It validates the type and size and
        // binds both into the signature, so the checks above are for fast
        // feedback only — the server stays the authority.
        const presigned = await api.post<PresignResponse>("/uploads/presign", {
          content_type: contentType,
          size_bytes: size,
          filename,
        });

        // PUT the bytes straight to storage.
        //
        // Deliberately carries NO Authorization header: the signature IS the
        // authorisation, and an extra header changes the signed request and is
        // rejected. Content-Type must match what was signed, byte for byte.
        const task = FileSystem.createUploadTask(
          presigned.upload_url,
          uri,
          {
            // A string union in this API, not an enum — unlike uploadType.
            httpMethod: "PUT",
            uploadType: FileSystem.FileSystemUploadType.BINARY_CONTENT,
            headers: { "Content-Type": presigned.content_type },
            // Foreground on iOS. A background session hands the upload to the
            // OS, which then delivers the response to a relaunched app — there
            // would be no `await` left here to receive the object key, and the
            // product would save without its photo.
            sessionType: FileSystem.FileSystemSessionType.FOREGROUND,
          },
          ({ totalBytesSent, totalBytesExpectedToSend }) => {
            if (totalBytesExpectedToSend > 0) {
              setProgress(
                Math.round((totalBytesSent / totalBytesExpectedToSend) * 100),
              );
            }
          },
        );

        const result = await task.uploadAsync();

        // Resolves undefined only if the task was cancelled, which nothing here
        // does — but the type admits it, and treating undefined as success
        // would save a product pointing at an object that was never stored.
        if (result === undefined || result === null) {
          throw new ApiError(
            0,
            "UPLOAD_CANCELLED",
            "The upload did not finish. Please try again.",
          );
        }

        if (result.status < 200 || result.status >= 300) {
          throw new ApiError(
            result.status,
            "UPLOAD_FAILED",
            "The file could not be uploaded. Please try again.",
          );
        }

        onChange([
          ...value,
          {
            kind: presigned.kind,
            objectKey: presigned.key,
            contentType: presigned.content_type,
            previewUrl: uri,
          },
        ]);
        setActive(value.length);
      } catch (caught) {
        setError(
          caught instanceof ApiError
            ? caught.message
            : "The file could not be uploaded. Please try again.",
        );
      } finally {
        setProgress(null);
      }
    },
    [onChange, value],
  );

  /** Downscales, strips EXIF, then uploads. */
  const uploadPhoto = useCallback(
    async (sourceUri: string) => {
      try {
        // Also strips EXIF, which quietly carries the GPS coordinates of the
        // supplier's farm into a public bucket.
        const rendered = await ImageManipulator.ImageManipulator.manipulate(sourceUri)
          .resize({ width: MAX_EDGE })
          .renderAsync();
        const processed = await rendered.saveAsync({
          compress: JPEG_QUALITY,
          format: ImageManipulator.SaveFormat.JPEG,
        });
        await upload(processed.uri, "image", "image/jpeg", "produce.jpg");
      } catch {
        setError("That photo could not be prepared. Please try another.");
      }
    },
    [upload],
  );

  /** Checks there is room before opening a picker. */
  const hasRoom = useCallback((): boolean => {
    if (value.length >= MAX_ITEMS) {
      setError(`A listing can have at most ${MAX_ITEMS} photos and videos.`);
      return false;
    }
    return true;
  }, [value.length]);

  /**
   * Asks for a permission and explains a refusal.
   *
   * A denied permission on iOS returns `canAskAgain: false` and every later
   * request resolves instantly as denied — the picker simply never opens, with
   * no visible reason. Sending the supplier to Settings is the only fix.
   */
  const withPermission = useCallback(
    async (
      request: () => Promise<ImagePicker.PermissionResponse>,
      deniedMessage: string,
      action: () => Promise<void>,
    ) => {
      const permission = await request();
      if (!permission.granted) {
        Alert.alert(
          "Permission needed",
          permission.canAskAgain
            ? deniedMessage
            : `${deniedMessage} You can turn it on in your phone's Settings, under Vayalavan Supplier.`,
        );
        return;
      }
      await action();
    },
    [],
  );

  const takePhoto = useCallback(() => {
    if (!hasRoom()) return;
    void withPermission(
      () => ImagePicker.requestCameraPermissionsAsync(),
      "Vayalavan needs the camera to photograph your produce.",
      async () => {
        const result = await ImagePicker.launchCameraAsync({
          mediaTypes: ["images"],
          // Square, matching the thumbnail every screen shows it in — cropping
          // here beats discovering later that the produce is off-frame.
          allowsEditing: true,
          aspect: [1, 1],
          quality: 1,
        });
        const asset = result.assets?.[0];
        if (!result.canceled && asset) await uploadPhoto(asset.uri);
      },
    );
  }, [hasRoom, uploadPhoto, withPermission]);

  const recordVideo = useCallback(() => {
    if (!hasRoom()) return;
    void withPermission(
      () => ImagePicker.requestCameraPermissionsAsync(),
      "Vayalavan needs the camera to film your produce.",
      async () => {
        const result = await ImagePicker.launchCameraAsync({
          mediaTypes: ["videos"],
          videoMaxDuration: MAX_VIDEO_SECONDS,
          // Medium, not high: this is the difference between a clip that
          // uploads over rural mobile data and one that does not.
          videoQuality: ImagePicker.UIImagePickerControllerQualityType.Medium,
        });
        const asset = result.assets?.[0];
        if (!result.canceled && asset) {
          await upload(
            asset.uri,
            "video",
            videoContentType(asset.uri, asset.mimeType),
            "produce.mp4",
          );
        }
      },
    );
  }, [hasRoom, upload, withPermission]);

  const chooseFromLibrary = useCallback(() => {
    if (!hasRoom()) return;
    void withPermission(
      () => ImagePicker.requestMediaLibraryPermissionsAsync(),
      "Vayalavan needs your photo library to attach pictures and videos to this listing.",
      async () => {
        const result = await ImagePicker.launchImageLibraryAsync({
          mediaTypes: ["images", "videos"],
          // No editing step: it only applies to images, and offering a crop
          // that silently does nothing for half the picks is worse than none.
          quality: 1,
        });
        const asset = result.assets?.[0];
        if (result.canceled || !asset) return;

        if (asset.type === "video") {
          await upload(
            asset.uri,
            "video",
            videoContentType(asset.uri, asset.mimeType),
            "produce.mp4",
          );
        } else {
          await uploadPhoto(asset.uri);
        }
      },
    );
  }, [hasRoom, upload, uploadPhoto, withPermission]);

  function remove(index: number) {
    onChange(value.filter((_, i) => i !== index));
  }

  /** Moves one item by `delta` places, clamped to the ends. */
  function move(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= value.length) return;
    const next = [...value];
    const [item] = next.splice(index, 1);
    if (item === undefined) return;
    next.splice(target, 0, item);
    onChange(next);
    setActive(target);
  }

  return (
    <View style={{ gap: spacing.md }}>
      {label !== "" && (
        <Text variant="label" tone="strong">
          {label}
        </Text>
      )}

      <View
        style={{
          height: 200,
          borderRadius: radius.card,
          overflow: "hidden",
          backgroundColor: colors.surface.sunken,
        }}
      >
        {current === undefined ? (
          <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
            <Text variant="caption" tone="faint">
              No photos yet
            </Text>
          </View>
        ) : current.kind === "video" ? (
          <VideoView
            player={player}
            style={{ width: "100%", height: "100%" }}
            contentFit="contain"
            nativeControls
            // expo-video 57 replaced the allowsFullscreen boolean with an
            // options object.
            fullscreenOptions={{ enable: true }}
          />
        ) : (
          <Image
            source={{ uri: current.previewUrl ?? "" }}
            resizeMode="contain"
            style={{ width: "100%", height: "100%" }}
            accessibilityLabel="Product photo"
          />
        )}
      </View>

      {value.length > 0 && (
        <>
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            contentContainerStyle={{ gap: spacing.xs }}
          >
            {value.map((item, index) => (
              <Pressable
                key={item.objectKey}
                onPress={() => setActive(index)}
                accessibilityRole="button"
                accessibilityState={{ selected: index === active }}
                accessibilityLabel={`${item.kind} ${index + 1} of ${value.length}${
                  index === coverIndex ? ", the cover" : ""
                }`}
                style={{
                  width: 64,
                  height: 64,
                  borderRadius: radius.card,
                  overflow: "hidden",
                  borderWidth: 2,
                  borderColor:
                    index === active ? colors.primary[600] : colors.surface.border,
                  backgroundColor: colors.surface.sunken,
                }}
              >
                {item.kind === "video" ? (
                  <View
                    style={{
                      flex: 1,
                      alignItems: "center",
                      justifyContent: "center",
                      backgroundColor: colors.primary[900],
                    }}
                  >
                    <Text variant="caption" tone="onPrimary">
                      ▶
                    </Text>
                  </View>
                ) : (
                  <Image
                    source={{ uri: item.previewUrl ?? "" }}
                    resizeMode="cover"
                    style={{ width: "100%", height: "100%" }}
                  />
                )}
                {coverBadge && index === coverIndex && (
                  <View
                    style={{
                      position: "absolute",
                      left: 0,
                      right: 0,
                      bottom: 0,
                      backgroundColor: colors.primary[700],
                      alignItems: "center",
                    }}
                  >
                    <Text variant="caption" tone="onPrimary">
                      Cover
                    </Text>
                  </View>
                )}
              </Pressable>
            ))}
          </ScrollView>

          <View style={{ flexDirection: "row", gap: spacing.sm }}>
            <View style={{ flex: 1 }}>
              <Button
                label="← Move"
                onPress={() => move(active, -1)}
                variant="ghost"
                size="sm"
                disabled={uploading || active === 0}
                block
              />
            </View>
            <View style={{ flex: 1 }}>
              <Button
                label="Move →"
                onPress={() => move(active, 1)}
                variant="ghost"
                size="sm"
                disabled={uploading || active >= value.length - 1}
                block
              />
            </View>
            <View style={{ flex: 1 }}>
              <Button
                label="Remove"
                onPress={() => remove(active)}
                variant="ghost"
                size="sm"
                disabled={uploading}
                block
              />
            </View>
          </View>
        </>
      )}

      <View style={{ gap: spacing.sm }}>
        <Button
          label="Take photo"
          onPress={takePhoto}
          variant="secondary"
          size="sm"
          disabled={uploading}
          block
        />
        <Button
          label="Record video"
          onPress={recordVideo}
          variant="secondary"
          size="sm"
          disabled={uploading}
          block
        />
        <Button
          label="Choose from library"
          onPress={chooseFromLibrary}
          variant="secondary"
          size="sm"
          disabled={uploading}
          block
        />
      </View>

      {uploading && (
        <View style={{ gap: spacing.xs }}>
          <View
            accessibilityRole="progressbar"
            accessibilityValue={{ now: progress, min: 0, max: 100 }}
            accessibilityLabel="Upload progress"
            style={{
              height: 6,
              borderRadius: radius.pill,
              backgroundColor: colors.surface.sunken,
              overflow: "hidden",
            }}
          >
            <View
              style={{
                height: "100%",
                width: `${progress}%`,
                backgroundColor: colors.primary[500],
              }}
            />
          </View>
          <Text variant="caption" tone="muted">
            Uploading… {progress}%
          </Text>
        </View>
      )}

      {error !== null && (
        <Text variant="caption" tone="danger" accessibilityRole="alert">
          {error}
        </Text>
      )}

      <Text variant="caption" tone="faint">
        {coverIndex === -1 && value.length > 0
          ? "Add a photo too — a video on its own leaves your listing without a cover picture on the shop page."
          : `Up to ${MAX_ITEMS} files. ${coverHint} Photos are resized before they are sent, so they upload quickly even on mobile data. Videos can be up to ${MAX_VIDEO_SECONDS} seconds.`}
      </Text>
    </View>
  );
}
