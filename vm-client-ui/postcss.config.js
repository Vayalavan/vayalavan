export default {
  plugins: {
    // Must run before tailwindcss so the @import of the ui-kit base
    // stylesheet is inlined and its @tailwind/@apply directives are
    // processed as part of this app's stylesheet.
    "postcss-import": {},
    tailwindcss: {},
    autoprefixer: {},
  },
};
