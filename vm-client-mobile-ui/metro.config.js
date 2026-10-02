/**
 * Metro bundler configuration.
 *
 * Expo's defaults, with one scoping change: the file watcher is confined to
 * this directory. This app is NOT an npm workspace member (see README, "Why
 * this is not an npm workspace"), so nothing above this folder belongs to it —
 * without this, Metro crawls the whole repository on every start, including
 * three web UIs, their node_modules and the Go services.
 *
 * Deliberately NOT set: `resolver.disableHierarchicalLookup`. Blocking the
 * ancestor lookup would stop Metro ever reaching the repository-root
 * node_modules, where React 18 lives for the web UIs — which sounds like a
 * useful guard until you notice every dependency this app needs is already a
 * direct dependency in its own node_modules, so the fallback is never taken.
 * Overriding it only fights the toolchain (expo-doctor flags it) for a
 * protection that has nothing to protect against.
 */
const { getDefaultConfig } = require("expo/metro-config");

const projectRoot = __dirname;
const config = getDefaultConfig(projectRoot);

config.watchFolders = [projectRoot];

module.exports = config;
