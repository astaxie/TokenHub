// Test-only endpoints. No production or developer API is a fallback.
module.exports = Object.freeze({
  apiOrigin: "http://tokenhub-ui.invalid",
  frontendPort: 43210,
  frontendOrigin: "http://127.0.0.1:43210",
  outputDirectory: "ui-test-results",
  distDirectory: ".next-ui",
});
