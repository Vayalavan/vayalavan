/**
 * Babel is configured by Expo's preset alone.
 *
 * Nothing else is added on purpose: every extra plugin here is a difference
 * between what Metro bundles and what `tsc` and the node:test suites see, and
 * that gap is where "works on my machine" comes from.
 *
 * `babel-preset-expo` is a direct devDependency even though `expo` already
 * depends on it. Babel resolves a preset named here relative to the PROJECT
 * root, and npm nested this one under `expo/node_modules`, where Babel does not
 * look — Metro then dies with "Cannot find module 'babel-preset-expo'" before
 * bundling a single file. Keep its version in step with the Expo SDK.
 */
module.exports = function babelConfig(api) {
  api.cache(true);
  return {
    presets: ["babel-preset-expo"],
  };
};
