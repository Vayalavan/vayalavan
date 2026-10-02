/**
 * The sweep across the rarest grade's chip.
 *
 * The web does this with a CSS keyframe behind Tailwind's `motion-safe:`
 * variant; a phone has neither, so both halves are done by hand — an Animated
 * loop, and a reduce-motion check that renders nothing at all.
 *
 * Animated rather than Reanimated: one translateX on one small view, and
 * Reanimated is a dependency both apps would have to take together against a
 * pinned Expo SDK (CLAUDE.md §3). `useNativeDriver` keeps the loop off the JS
 * thread, so it does not stutter while the product screen is scrolling.
 *
 * Decoration only — `accessibilityElementsHidden` and no text. The tier is
 * named in words on the chip beside it, because a customer who cannot see the
 * gold has to be told the same thing.
 */
import { useEffect, useRef, useState, type ReactElement } from "react";
import { AccessibilityInfo, Animated, Easing, View } from "react-native";

import { colors } from "../theme/tokens";

/** One cycle: the sweep, then the pause before the next. */
const CYCLE_MS = 2400;

export function RarityShimmer({ width }: { width: number }): ReactElement | null {
  const [reduceMotion, setReduceMotion] = useState(true);
  const progress = useRef(new Animated.Value(0)).current;

  // Start pessimistic and turn the animation ON once the setting is known,
  // rather than animating for a frame and stopping: someone who asked for
  // less motion should not see any.
  useEffect(() => {
    let cancelled = false;
    void AccessibilityInfo.isReduceMotionEnabled().then((enabled) => {
      if (!cancelled) setReduceMotion(enabled);
    });
    const sub = AccessibilityInfo.addEventListener("reduceMotionChanged", setReduceMotion);
    return () => {
      cancelled = true;
      sub.remove();
    };
  }, []);

  useEffect(() => {
    if (reduceMotion) return;
    const loop = Animated.loop(
      Animated.timing(progress, {
        toValue: 1,
        duration: CYCLE_MS,
        easing: Easing.inOut(Easing.ease),
        useNativeDriver: true,
      }),
    );
    loop.start();
    return () => {
      loop.stop();
      progress.setValue(0);
    };
  }, [progress, reduceMotion]);

  if (reduceMotion) return null;

  // Half the chip, not a third. On a 108pt cell a third was a flicker easy to
  // miss entirely, and a glint nobody catches is the same as no glint.
  const band = Math.max(32, Math.round(width / 2));

  return (
    <Animated.View
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
      pointerEvents="none"
      style={{
        position: "absolute",
        top: 0,
        bottom: 0,
        left: -band,
        width: band,
        transform: [
          {
            translateX: progress.interpolate({
              // The sweep takes the first third of the cycle and the rest is
              // the pause. A continuous shimmer reads as a loading skeleton,
              // which is the one thing this must not look like.
              inputRange: [0, 0.35, 1],
              outputRange: [0, width + band, width + band],
            }),
          },
        ],
      }}
    >
      {/* Three bands standing in for a gradient: expo-linear-gradient would be
          a dependency for one decoration, and a soft edge either side of a
          brighter middle is what the CSS gradient amounts to anyway. */}
      <View style={{ flex: 1, flexDirection: "row" }}>
        <View style={{ flex: 1, backgroundColor: colors.gold[100], opacity: 0.5 }} />
        <View style={{ flex: 1, backgroundColor: colors.surface.raised, opacity: 0.95 }} />
        <View style={{ flex: 1, backgroundColor: colors.gold[100], opacity: 0.5 }} />
      </View>
    </Animated.View>
  );
}
