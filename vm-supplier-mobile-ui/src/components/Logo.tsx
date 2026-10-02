/**
 * The Vayalavan lockup.
 *
 * The master artwork lives in `packages/vm-ui-kit/src/logo/` (CLAUDE.md §9);
 * `make -C infra sync-brand` copies it into this app's assets/ alongside the
 * three web UIs' public/ folders, so there is still exactly one file to change
 * when the brand changes.
 *
 * The web VayalLockup also offers a mark-only and a mono variant. Neither is
 * built here because neither has a place to appear: the app icon is the mark,
 * and nothing in a phone app is printed.
 */
import type { ReactElement } from "react";
import { Image } from "react-native";

const LOCKUP = require("../../assets/logo-lockup.png") as number;

const SIZES = { sm: 48, md: 72, lg: 112 } as const;

export function VayalLockup({
  size = "md",
  decorative = false,
}: {
  size?: keyof typeof SIZES;
  /**
   * Set when the logo sits next to a visible "Vayalavan" label, so a
   * screen reader does not read the name twice.
   */
  decorative?: boolean;
}): ReactElement {
  const dimension = SIZES[size];

  return (
    <Image
      source={LOCKUP}
      style={{ width: dimension, height: dimension }}
      resizeMode="contain"
      accessible={!decorative}
      // The logo IS the brand name, so it must be announced unless something
      // else on screen already says it.
      {...(decorative
        ? { accessibilityElementsHidden: true, importantForAccessibility: "no-hide-descendants" as const }
        : { accessibilityRole: "image" as const, accessibilityLabel: "Vayalavan" })}
    />
  );
}
