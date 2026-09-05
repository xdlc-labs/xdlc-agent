/** Curated docs from ui/content/docs (synced from repo docs/ via `bun run sync-docs`). */
import install from "../../content/docs/install.md?raw";
import gettingStarted from "../../content/docs/getting-started.md?raw";
import apiTokens from "../../content/docs/api-tokens.md?raw";
import productionLoop from "../../content/docs/production-loop.md?raw";
import githubWebhooks from "../../content/docs/github-webhooks.md?raw";
import fixModes from "../../content/docs/fix-modes.md?raw";
import rulesAndSkills from "../../content/docs/rules-and-skills.md?raw";
import sessions from "../../content/docs/sessions.md?raw";
import gitopsArgo from "../../content/docs/gitops-argo.md?raw";
import prodHealth from "../../content/docs/prod-health.md?raw";
import deployment from "../../content/docs/deployment.md?raw";
import operations from "../../content/docs/operations.md?raw";
import configuration from "../../content/docs/configuration.md?raw";
import consoleDoc from "../../content/docs/console.md?raw";
import architecture from "../../content/docs/architecture.md?raw";
import apiReference from "../../content/docs/api-reference.md?raw";
import security from "../../content/docs/SECURITY.md?raw";
import contributing from "../../content/docs/CONTRIBUTING.md?raw";
import vsAlternatives from "../../content/docs/vs-alternatives.md?raw";
import whyNotGha from "../../content/docs/why-not-github-action.md?raw";

const bySlug: Record<string, string> = {
  install,
  "getting-started": gettingStarted,
  "api-tokens": apiTokens,
  "production-loop": productionLoop,
  "github-webhooks": githubWebhooks,
  "fix-modes": fixModes,
  "rules-and-skills": rulesAndSkills,
  sessions,
  "gitops-argo": gitopsArgo,
  "prod-health": prodHealth,
  deployment,
  operations,
  configuration,
  console: consoleDoc,
  architecture,
  "api-reference": apiReference,
  SECURITY: security,
  CONTRIBUTING: contributing,
  "vs-alternatives": vsAlternatives,
  "why-not-github-action": whyNotGha,
};

export function getDocMarkdown(slug: string): string | undefined {
  return bySlug[slug];
}

export function hasDoc(slug: string): boolean {
  return Object.hasOwn(bySlug, slug);
}
