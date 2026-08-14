import { themes as prismThemes } from "prism-react-renderer";

export default {
  title: "OCR Platform",
  tagline: "Integration and operations documentation",
  favicon: "img/favicon.svg",
  url: process.env.PUBLIC_DOCS_BASE_URL || "https://ocr.dungxbuif.com",
  baseUrl: "/",
  onBrokenLinks: "throw",
  markdown: { hooks: { onBrokenMarkdownLinks: "throw" } },
  i18n: { defaultLocale: "en", locales: ["en"] },
  presets: [
    [
      "classic",
      {
        docs: {
          path: "../docs",
          routeBasePath: "/",
          sidebarPath: "./sidebars.js",
          showLastUpdateTime: true,
          exclude: [
            "architecture/**",
            "configuration/**",
            "deployment/**",
            "planning/**",
            "testing/**",
            "guides/local-development.md",
          ],
        },
        blog: false,
        theme: { customCss: "./src/css/custom.css" },
      },
    ],
  ],
  themeConfig: {
    colorMode: { defaultMode: "light", disableSwitch: true, respectPrefersColorScheme: false },
    navbar: {
      title: "OCR Platform",
      items: [
        { to: "/guides/onboarding", label: "Onboarding", position: "left" },
        { to: "/api/API_REFERENCE", label: "API", position: "left" },
        { to: "/api/OCR_RESPONSE", label: "OCR response", position: "left" },
        { to: "/api/MCP_INTEGRATION", label: "MCP", position: "left" },
        { href: "/api/v1/docs", label: "Swagger", position: "right", target: "_blank" },
      ],
    },
    footer: {
      style: "light",
      links: [
        { title: "Integrate", items: [{ label: "Quickstart", to: "/guides/onboarding" }, { label: "API reference", to: "/api/API_REFERENCE" }] },
        { title: "Understand", items: [{ label: "OCR response", to: "/api/OCR_RESPONSE" }, { label: "MCP", to: "/api/MCP_INTEGRATION" }] },
      ],
      copyright: `OCR Platform documentation · ${new Date().getFullYear()}`,
    },
    prism: { theme: prismThemes.github, darkTheme: prismThemes.dracula },
  },
};
