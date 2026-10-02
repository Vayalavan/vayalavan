/**
 * Native entry point.
 *
 * registerRootComponent does what AppRegistry.registerComponent does, plus the
 * Expo-specific setup that has to happen before the first render. Keep this
 * file to one job — anything else added here runs before the error boundary in
 * App.tsx exists to catch it.
 */
import { registerRootComponent } from "expo";

import { App } from "./src/App";

registerRootComponent(App);
