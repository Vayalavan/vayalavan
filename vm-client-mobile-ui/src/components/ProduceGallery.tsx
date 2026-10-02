/**
 * The product detail viewer: one large image or video at a time, with a
 * thumbnail strip when the grower attached more than one.
 *
 * Every item is a full-width page of one horizontal list, so moving between
 * them is a SLIDE — swiped with a thumb, or animated when a thumbnail is
 * tapped. The web version puts big arrows on the picture for the same job;
 * here the gesture IS the control, and arrows over a 280pt image would cover
 * the produce a customer opened the screen to look at. The counter says how
 * many there are, which is what tells someone there is anything to swipe to.
 *
 * Videos are muted, and a clip starts itself only once the customer has STAYED
 * on it for AUTOPLAY_DELAY_MS. Swiping through a gallery to reach the photo on
 * the far side should not start three clips on the way past, and a customer on
 * mobile data has not asked to download fifty megabytes — the bytes move when
 * they stop and look. Only the page in view has a player at all, so sliding
 * away from a clip stops it rather than leaving it running off-screen.
 *
 * When the gallery is empty this falls back to ProduceImage with the cover
 * URL, which is exactly what every catalogue card shows. That keeps the
 * no-photo, expired-URL and sold-out treatments in ONE place rather than
 * reimplementing them here.
 */
import { useEffect, useRef, useState, type ReactElement } from "react";
import {
  FlatList,
  Image,
  Modal,
  Pressable,
  ScrollView,
  View,
  useWindowDimensions,
  type NativeScrollEvent,
  type NativeSyntheticEvent,
} from "react-native";
import { useVideoPlayer, VideoView } from "expo-video";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { colors, radius, spacing } from "../theme/tokens";
import { ProduceImage } from "./ProduceImage";
import { Text } from "./Text";

/**
 * How long a video has to be the page in view before it plays itself.
 *
 * The same second the web gallery waits, for the same reason: passing through
 * a clip on the way to a photograph should cost nothing, and stopping to look
 * at one should not feel like waiting for it. The timer is cancelled on every
 * swipe, so it only ever fires for a page the customer settled on.
 */
const AUTOPLAY_DELAY_MS = 1000;

export interface GalleryMedia {
  id: string;
  kind: "image" | "video";
  /** A short-lived presigned GET, minted per response. Do not cache. */
  url: string | null;
  content_type: string | null;
}

export interface ProduceGalleryProps {
  media: GalleryMedia[];
  /** The cover — what a catalogue card shows — used when media is empty. */
  coverUrl: string | null;
  name: string;
  height: number;
  soldOut?: boolean;
  stockHint?: string;
}

export function ProduceGallery({
  media,
  coverUrl,
  name,
  height,
  soldOut = false,
  stockHint = "",
}: ProduceGalleryProps): ReactElement {
  const items = media.filter((item) => item.url !== null && item.url !== "");
  const [active, setActive] = useState(0);
  // Which still is open full-screen, or null. A grower's comparison chart has
  // six rows of small print, and a 350pt square is somewhere to see it, not
  // read it.
  const [viewing, setViewing] = useState<number | null>(null);
  const pages = useRef<FlatList<GalleryMedia>>(null);

  // The page width has to be the LIST's width, not the window's: this sits
  // inside a card whose padding the window knows nothing about. The window is
  // only the first-render guess, replaced on layout.
  const window = useWindowDimensions();
  const [width, setWidth] = useState(window.width);

  // A different product can arrive on the same screen. Without this the new
  // gallery opens on the previous one's index — and, worse, on a scroll
  // offset that no longer has a page under it.
  useEffect(() => {
    setActive(0);
    pages.current?.scrollToOffset({ offset: 0, animated: false });
  }, [media]);

  const index = Math.min(active, Math.max(items.length - 1, 0));
  const current = items[index];

  // The hook must run on every render, so the player is created whether or not
  // the page in view is a video. A null source is a player with nothing
  // loaded, which costs nothing.
  const videoSource = current?.kind === "video" ? (current.url ?? null) : null;
  const player = useVideoPlayer(videoSource, (instance) => {
    // Explicit rather than relying on the default: a produce page that starts
    // playing on its own, with sound, in a shop is the worst version of this
    // feature.
    instance.loop = false;
    instance.muted = true;
  });

  // Play the clip the customer settled on, muted, after the delay above.
  //
  // Muted is not a preference: a produce page that starts making noise in a
  // shop is the worst version of this feature, and the player is created muted
  // for that reason.
  //
  // Nothing here pauses on the way out, and that is the fix for a crash rather
  // than an omission. `useVideoPlayer` hands back a RELEASING shared object:
  // it drops the native player and builds a new one whenever the source
  // changes, and swiping to another page changes the source. The cleanup then
  // ran against a JavaScript handle whose native counterpart was already gone,
  // which threw `NotFoundException: Unable to find the native shared object`
  // and took the screen down. Releasing the player stops playback by itself,
  // so the pause was never doing work — only finding the hole.
  useEffect(() => {
    if (current?.kind !== "video") return;
    const timer = setTimeout(() => {
      try {
        player.play();
      } catch {
        // Released between arming this timer and it firing. That means the
        // customer moved on, which is precisely the state we wanted anyway —
        // there is no clip left to start.
      }
    }, AUTOPLAY_DELAY_MS);
    return () => clearTimeout(timer);
  }, [current?.id, current?.kind, player]);

  function onSettled(event: NativeSyntheticEvent<NativeScrollEvent>): void {
    if (width <= 0) return;
    const page = Math.round(event.nativeEvent.contentOffset.x / width);
    setActive(Math.max(0, Math.min(page, items.length - 1)));
  }

  /** Moves to one page, animated — the tap equivalent of a swipe. */
  function show(next: number): void {
    setActive(next);
    pages.current?.scrollToIndex({ index: next, animated: true });
  }

  if (!current) {
    return (
      <ProduceImage
        uri={coverUrl}
        name={name}
        height={height}
        soldOut={soldOut}
        stockHint={stockHint}
      />
    );
  }

  return (
    <View onLayout={(event) => setWidth(event.nativeEvent.layout.width)}>
      <View>
        <FlatList
          ref={pages}
          data={items}
          keyExtractor={(item) => item.id}
          horizontal
          pagingEnabled
          showsHorizontalScrollIndicator={false}
          onMomentumScrollEnd={onSettled}
          // Every page is exactly the frame's width, so the list can jump to
          // one without measuring — and scrollToIndex cannot fail on a page
          // that has not been rendered yet.
          getItemLayout={(_, itemIndex) => ({
            length: width,
            offset: width * itemIndex,
            index: itemIndex,
          })}
          renderItem={({ item, index: itemIndex }) => (
            <View style={{ width, height }}>
              {item.kind === "video" ? (
                itemIndex === index ? (
                  <View style={{ flex: 1, backgroundColor: colors.primary[900] }}>
                    <VideoView
                      player={player}
                      style={{ width: "100%", height: "100%" }}
                      contentFit="contain"
                      nativeControls
                      // expo-video 57 replaced the allowsFullscreen boolean
                      // with an options object.
                      fullscreenOptions={{ enable: true }}
                      accessibilityLabel={`Video of ${name}`}
                    />
                  </View>
                ) : (
                  // Off-screen pages get a placeholder rather than a second
                  // player: one clip at a time is the whole point of stopping
                  // the one that slides away.
                  <View
                    style={{
                      flex: 1,
                      alignItems: "center",
                      justifyContent: "center",
                      backgroundColor: colors.primary[900],
                    }}
                  >
                    <Text variant="display" tone="onPrimary">
                      ▶
                    </Text>
                  </View>
                )
              ) : (
                <Pressable
                  onPress={() => setViewing(itemIndex)}
                  accessibilityRole="imagebutton"
                  accessibilityHint="Opens the picture full screen"
                >
                  <ProduceImage
                    uri={item.url}
                    name={name}
                    height={height}
                    soldOut={soldOut}
                    // The nudge belongs to the produce, not to a picture of
                    // it, so it rides on the first page only rather than
                    // repeating on every swipe.
                    stockHint={itemIndex === 0 ? stockHint : ""}
                  />
                </Pressable>
              )}
            </View>
          )}
        />

        {items.length > 1 && (
          // Where you are, as dots: a counter says it in numbers, but dots
          // are what every other gallery on the handset uses to say "swipe".
          <View
            pointerEvents="none"
            accessibilityElementsHidden
            importantForAccessibility="no-hide-descendants"
            style={{
              position: "absolute",
              bottom: spacing.md,
              alignSelf: "center",
              flexDirection: "row",
              alignItems: "center",
              gap: 6,
              paddingHorizontal: 10,
              paddingVertical: 7,
              borderRadius: radius.pill,
              overflow: "hidden",
            }}
          >
            {/* The scrim as its own layer: an opacity on the pill itself
                would fade the dots with it, and React Native has no slash
                opacity for a token colour. */}
            <View
              style={{
                ...ABSOLUTE_FILL,
                backgroundColor: colors.primary[950],
                opacity: 0.55,
              }}
            />
            {items.map((item, dotIndex) => (
              <View
                key={item.id}
                style={{
                  height: 6,
                  width: dotIndex === index ? 16 : 6,
                  borderRadius: 3,
                  backgroundColor: colors.surface.raised,
                  opacity: dotIndex === index ? 1 : 0.5,
                }}
              />
            ))}
          </View>
        )}
      </View>

      {items.length > 1 && (
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          contentContainerStyle={{
            gap: spacing.sm,
            padding: spacing.md,
          }}
        >
          {items.map((item, thumbIndex) => (
            <Pressable
              key={item.id}
              onPress={() => show(thumbIndex)}
              accessibilityRole="button"
              accessibilityState={{ selected: thumbIndex === index }}
              accessibilityLabel={`Show ${item.kind} ${thumbIndex + 1} of ${items.length}`}
              style={{
                width: 64,
                height: 64,
                borderRadius: radius.card,
                overflow: "hidden",
                borderWidth: 2,
                borderColor:
                  thumbIndex === index ? colors.primary[600] : colors.surface.border,
                backgroundColor: colors.surface.sunken,
                opacity: thumbIndex === index ? 1 : 0.65,
              }}
            >
              {item.kind === "video" ? (
                // A still frame of a video needs decoding, which this app does
                // not do. A labelled tile says what it is without pretending
                // to preview it.
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
                  source={{ uri: item.url ?? "" }}
                  resizeMode="cover"
                  style={{ width: "100%", height: "100%" }}
                />
              )}
            </Pressable>
          ))}
        </ScrollView>
      )}

      <FullScreenViewer
        items={items}
        start={viewing}
        name={name}
        onClose={() => setViewing(null)}
      />
    </View>
  );
}

const ABSOLUTE_FILL = {
  position: "absolute",
  top: 0,
  right: 0,
  bottom: 0,
  left: 0,
} as const;

/**
 * Stills, full-screen, swipeable. Videos are skipped rather than shown: the
 * player on the page already has its own native full-screen button, and a
 * second player here would be a second clip playing.
 */
function FullScreenViewer({
  items,
  start,
  name,
  onClose,
}: {
  items: GalleryMedia[];
  start: number | null;
  name: string;
  onClose: () => void;
}): ReactElement {
  const window = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const stills = items.filter((item) => item.kind === "image");
  const startId = start === null ? undefined : items[start]?.id;
  const initial = Math.max(0, stills.findIndex((item) => item.id === startId));
  const [page, setPage] = useState(initial);

  useEffect(() => {
    setPage(initial);
  }, [initial, start]);

  return (
    <Modal
      visible={start !== null}
      animationType="fade"
      onRequestClose={onClose}
      statusBarTranslucent
    >
      <View style={{ flex: 1, backgroundColor: colors.primary[950] }}>
        <FlatList
          data={stills}
          keyExtractor={(item) => item.id}
          horizontal
          pagingEnabled
          showsHorizontalScrollIndicator={false}
          initialScrollIndex={initial}
          getItemLayout={(_, itemIndex) => ({
            length: window.width,
            offset: window.width * itemIndex,
            index: itemIndex,
          })}
          onMomentumScrollEnd={(event) =>
            setPage(Math.round(event.nativeEvent.contentOffset.x / window.width))
          }
          renderItem={({ item }) => (
            <View
              style={{
                width: window.width,
                height: window.height,
                alignItems: "center",
                justifyContent: "center",
                paddingHorizontal: spacing.sm,
              }}
            >
              <Image
                source={{ uri: item.url ?? "" }}
                resizeMode="contain"
                accessibilityLabel={name}
                style={{ width: "100%", height: "80%", borderRadius: radius.card }}
              />
            </View>
          )}
        />

        <View
          style={{
            position: "absolute",
            top: insets.top + spacing.sm,
            left: spacing.lg,
            right: spacing.lg,
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <Text variant="label" tone="onPrimary" tabular>
            {stills.length > 1 ? `${page + 1} / ${stills.length}` : ""}
          </Text>
          <Pressable
            onPress={onClose}
            accessibilityRole="button"
            accessibilityLabel="Close"
            hitSlop={12}
            style={{
              width: 40,
              height: 40,
              borderRadius: 20,
              alignItems: "center",
              justifyContent: "center",
              backgroundColor: colors.surface.raised,
            }}
          >
            <Text variant="title" tone="strong">
              ×
            </Text>
          </Pressable>
        </View>
      </View>
    </Modal>
  );
}
