import type { APIRoute } from 'astro';

// Emits /llms.txt (the emerging AI-discovery convention): a curated, plain-text
// map of the docs so an LLM can find the canonical page for a question. Built
// from Astro.site, so the links follow the configured canonical domain.
export const GET: APIRoute = ({ site }) => {
  const base = (site?.href ?? 'https://poddle.dev/').replace(/\/$/, '');
  const link = (path: string, label: string, desc: string) =>
    `- [${label}](${base}${path}): ${desc}`;

  const body = `# poddle

> Secret-safe dev sandboxes for coding agents. poddle is a secretless credential broker: a pod holds only a revocable \`poddle_…\` handle, and the broker injects the real key on the wire while scrubbing stray secrets from outbound request bodies — so the raw secret never lands in the pod.

## Docs
${[
  link('/docs', 'Getting started', 'install, add an identity, and run a coding agent in a fresh secretless pod'),
  link('/docs/examples', 'Examples', 'end-to-end terminals: headless tasks, up/ls/down, remote hosts'),
  link('/docs/concepts', 'Concepts', 'the five ideas — identity, provider, harness, pod, broker'),
  link('/docs/security', 'Security & threat model', 'isolation, credential injection, egress redaction, and exactly what an attacker gets'),
  link('/docs/connectors', 'Connectors', 'broker services (git, CI, databases) into a pod without the token entering it'),
  link('/docs/templates', 'Templates', 'reproducible pod blueprints in TOML — image, repo, connectors, safety rules'),
  link('/docs/headless', 'Headless & CI', 'poddle task, burst-and-shrink sizing, reactive autoscaling, secret-safe pipelines'),
  link('/docs/configuration', 'Configuration', 'on-disk layout, template resolution and merging, every environment variable'),
  link('/docs/faq', 'FAQ & troubleshooting', 'Podman setup, remote hosts, template/identity errors, secret storage'),
  link('/docs/commands', 'Command reference', 'every CLI command with usage, flags, and examples, generated from the binary'),
].join('\n')}

## More
${[
  link('/security', 'Security overview', 'the injection/redaction model and how a request is handled'),
  link('/connectors', 'Connector catalog', 'the built-in connector types'),
  link('/pricing', 'Pricing', 'plans and pod-compute options'),
].join('\n')}
`;

  return new Response(body, {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });
};
