import { defineConfig } from "@rspress/core";
import { pluginSitemap } from "@rspress/plugin-sitemap";
import path from "node:path";
import pluginKatex from "rspress-plugin-katex";

export default defineConfig({
  root: "content",
  base: "/hclapi/",
  lang: "en",
  title: "hclapi",
  description:
    "A declarative runtime that turns HCL manifests into HTTP APIs.",
  logoText: "hclapi",
  outDir: "dist",
  globalStyles: path.join(__dirname, "theme/index.css"),
  head: [["meta", { name: "theme-color", content: "#d97706" }]],
  plugins: [
    pluginSitemap({
      siteUrl: "https://ju4n97.github.io/hclapi/",
    }),
    pluginKatex(),
  ],
  llms: true,
  themeConfig: {
    enableContentAnimation: false,
    enableAppearanceAnimation: false,
    lastUpdated: true,
    enableScrollToTop: true,
    llmsUI: true,
    editLink: {
      docRepoBaseUrl: "https://github.com/ju4n97/hclapi/edit/main/",
    },
    socialLinks: [
      {
        icon: "github",
        mode: "link",
        content: "https://github.com/ju4n97/hclapi",
      },
    ],
    footer: {
      message:
        '<a href="https://github.com/ju4n97/hclapi/blob/main/LICENSE" target="_blank" rel="noreferrer">MIT License</a> © 2026 hclapi contributors.',
    },
  },
  route: {
    useTransitions: false,
  },
});