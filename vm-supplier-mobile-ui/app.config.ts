/**
 * Expo app configuration.
 *
 * This file runs in Node at build time (and when `expo start` boots), which is
 * what makes it the right place to enforce CLAUDE.md rule 3: everything
 * configurable comes from env vars, and a missing one fails fast with a clear
 * message. On a phone "fail fast" has to mean *at build time* — a native
 * binary cannot be reconfigured after the fact, and a runtime throw is a white
 * screen on a supplier's handset at 6am.
 *
 * Environment comes from the repository's single root .env (the same file the
 * gateway and the three web UIs read — see .env.example), not from a copy in
 * this directory. One file for the whole platform.
 */
import { config as loadDotenv } from "dotenv";
import type { ConfigContext, ExpoConfig } from "expo/config";
import path from "node:path";

// __dirname, not import.meta: Expo transpiles this file to CommonJS before
// evaluating it, and import.meta is a syntax error there.
const repoRoot = path.resolve(__dirname, "..");

// `override: false` (the default) means a variable already exported in the
// shell wins over the file — which is how CI and EAS supply their own values.
loadDotenv({ path: path.join(repoRoot, ".env") });

/** Reads a required variable, or stops the build saying exactly which. */
function required(name: string): string {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    throw new Error(
      `Missing required environment variable ${name}.\n` +
        `Set it in ${path.join(repoRoot, ".env")} — copy .env.example if you have not yet.\n` +
        `See vm-supplier-mobile-ui/README.md for what each mobile variable does.`,
    );
  }
  return value.trim();
}

/** Reads an optional variable, returning undefined rather than "". */
function optional(name: string): string | undefined {
  const value = process.env[name]?.trim();
  return value ? value : undefined;
}

/** Loopback, or one of the RFC1918 private ranges — i.e. a developer's LAN. */
function isPrivateHost(hostname: string): boolean {
  return (
    hostname === "localhost" ||
    hostname === "127.0.0.1" ||
    hostname === "::1" ||
    /^10\./.test(hostname) ||
    /^192\.168\./.test(hostname) ||
    /^172\.(1[6-9]|2\d|3[01])\./.test(hostname)
  );
}

/**
 * Checks the API URL against the gateway this repository actually runs.
 *
 * Worth doing at build time because the value is baked into the binary: a wrong
 * one is not a config mistake you notice, it is an app that sits there for
 * thirty seconds and then says "that took too long", on a phone, with nothing
 * pointing at the cause. Both mistakes below have already been made once.
 *
 * Only private addresses are checked. A production HTTPS host is none of this
 * function's business — it cannot know what is in front of it.
 */
function validateApiBaseUrl(raw: string): string {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new Error(
      `MOBILE_API_BASE_URL is not a valid URL: ${JSON.stringify(raw)}\n` +
        `Expected something like http://192.168.1.20:8080/api`,
    );
  }

  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error(
      `MOBILE_API_BASE_URL must be http or https, got "${url.protocol}" in ${raw}`,
    );
  }

  if (!isPrivateHost(url.hostname)) return raw;

  // A LAN address with no port means port 80, and the gateway is not there.
  // Compare against the port this repo's own gateway is configured on rather
  // than guessing.
  const gatewayPort = optional("GATEWAY_PORT");
  const effectivePort = url.port !== "" ? url.port : url.protocol === "https:" ? "443" : "80";

  if (gatewayPort !== undefined && effectivePort !== gatewayPort) {
    throw new Error(
      `MOBILE_API_BASE_URL points at ${url.hostname}:${effectivePort}, but this ` +
        `repository's gateway runs on port ${gatewayPort} (GATEWAY_PORT).\n` +
        (url.port === ""
          ? `The URL has no port, so it means port ${effectivePort}. ` +
            `Add the port: http://${url.hostname}:${gatewayPort}${url.pathname}\n`
          : `Set MOBILE_API_BASE_URL=http://${url.hostname}:${gatewayPort}${url.pathname}\n`) +
        `If you really are fronting the gateway with a proxy on ${effectivePort}, ` +
        `change GATEWAY_PORT to match or point this at the proxy's host.`,
    );
  }

  if (!url.pathname.replace(/\/+$/, "").endsWith("/api")) {
    // Not fatal — a proxy could mount it elsewhere — but it is nearly always
    // an omission, and every request 404s without it.
    console.warn(
      `[vayal] MOBILE_API_BASE_URL is "${raw}", which does not end in /api. ` +
        `The gateway serves the JSON API under /api; without it every request 404s.`,
    );
  }

  if (url.hostname === "localhost" || url.hostname === "127.0.0.1") {
    console.warn(
      `[vayal] MOBILE_API_BASE_URL is ${raw}. On a handset "localhost" is the ` +
        `handset itself, so this only works in a simulator on this machine. ` +
        `Use this Mac's LAN address for a real device (ipconfig getifaddr en0).`,
    );
  }

  return raw;
}

/**
 * True when this is a local development build.
 *
 * Gates exactly one thing: permission to talk to a plain-HTTP gateway on the
 * LAN. Android blocks cleartext by default and iOS blocks it via ATS, both
 * correctly — production traffic is HTTPS. But `make dev` serves the gateway
 * on http://<lan-ip>:8080, so a development build that cannot reach it is
 * useless. Keyed off APP_ENV so a production build can never accidentally
 * ship the exemption.
 */
const isDevelopment = (process.env.APP_ENV ?? "development") !== "production";

const IOS_BUNDLE_ID = "com.vayalmikrogreenz.supplier";
const ANDROID_PACKAGE = "com.vayalmikrogreenz.supplier";

export default ({ config }: ConfigContext): ExpoConfig => ({
  ...config,
  name: "Vayalavan Supplier",
  slug: "vayal-supplier",
  scheme: "vayalsupplier",
  version: "0.1.0",

  // Android and iOS only. Expo's default includes "web", which makes `expo
  // start` offer a web bundle and then fail on a missing `react-native-web` —
  // a renderer this app would never ship, because the web supplier portal
  // already exists as vm-supplier-ui and is the better thing on a desktop.
  platforms: ["ios", "android"],

  orientation: "portrait",
  userInterfaceStyle: "light",
  icon: "./assets/icon.png",
  // Matches surface.DEFAULT in the Vayal palette. Duplicated here because
  // app.json cannot import TypeScript; theme/tokens.test.ts asserts the two
  // agree so this cannot drift unnoticed.
  backgroundColor: "#faf9f6",

  assetBundlePatterns: ["**/*"],

  ios: {
    bundleIdentifier: IOS_BUNDLE_ID,
    supportsTablet: true,
    infoPlist: {
      // Suppliers photograph produce in a field; the app must keep working
      // with the screen showing a long availability list.
      UIRequiresFullScreen: false,
      ...(isDevelopment && {
        NSAppTransportSecurity: { NSAllowsLocalNetworking: true },
      }),
    },
  },

  android: {
    package: ANDROID_PACKAGE,
    // expo-image-picker's plugin adds RECORD_AUDIO because the same picker can
    // record video. This app only ever takes a still photograph of produce, and
    // a Play Store listing that asks a farmer for microphone access to sell
    // tomatoes is a trust problem before it is a technical one.
    blockedPermissions: ["android.permission.RECORD_AUDIO"],
    adaptiveIcon: {
      foregroundImage: "./assets/adaptive-icon.png",
      backgroundColor: "#faf9f6",
    },
    // Edge-to-edge is the default from SDK 57 and the opt-in flag is gone;
    // screens handle the inset themselves (see components/Screen.tsx).
    ...(isDevelopment && { usesCleartextTraffic: true }),
  },

  plugins: [
    // No custom fonts are bundled — the app uses the system stack, matching
    // the web preset. This is here because @expo/vector-icons depends on
    // expo-font, and `npx expo install` asks for the plugin alongside it.
    "expo-font",
    "expo-secure-store",
    // Both required as plugins from SDK 57 — `expo install --fix` asks for
    // them by name. They were implicit before.
    "expo-sharing",
    "expo-status-bar",
    [
      "expo-image-picker",
      {
        photosPermission:
          "Vayalavan needs your photo library so you can attach pictures and videos of your produce to a listing.",
        cameraPermission:
          "Vayalavan needs the camera so you can photograph or film your produce for a listing.",
        // Recording a clip needs the microphone as well as the camera. Without
        // this the video picker opens and then fails on iOS with no reason
        // given.
        microphonePermission:
          "Vayalavan needs the microphone so your produce videos record with sound.",
      },
    ],
    // Lets a grower watch back the clip they just filmed before saving the
    // listing. Included in Expo Go for SDK 54, so no development build is
    // needed to test it on a handset.
    "expo-video",
    [
      "expo-splash-screen",
      {
        image: "./assets/splash-icon.png",
        resizeMode: "contain",
        backgroundColor: "#faf9f6",
      },
    ],
  ],

  /**
   * Baked into the app manifest at build time and read at runtime through
   * expo-constants (see src/lib/config.ts).
   *
   * Everything here is PUBLIC — it ships inside the binary and can be read
   * out of it. Never put a secret in this block. The supplier app needs no
   * secret: it authenticates with the supplier's own password and holds only
   * their tokens.
   */
  extra: {
    apiBaseUrl: validateApiBaseUrl(required("MOBILE_API_BASE_URL")),
    supportEmail: required("SUPPORT_EMAIL"),
    appEnv: process.env.APP_ENV ?? "development",
    eas: {
      // Set once `eas init` has linked the project. Absent is fine for
      // `expo start` and local `run:android` / `run:ios`.
      projectId: optional("MOBILE_EAS_PROJECT_ID"),
    },
  },
});
