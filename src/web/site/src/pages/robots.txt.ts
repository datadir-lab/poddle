import type { APIRoute } from 'astro';

// Emits /robots.txt from Astro.site so the Sitemap/llms.txt URLs always match
// the canonical domain. AI crawlers are welcome — for a developer tool, being
// cited in AI answers is a net benefit, so we allow all and only point the way.
export const GET: APIRoute = ({ site }) => {
  const base = (site?.href ?? 'https://poddle.dev/').replace(/\/$/, '');
  const body = `# poddle — all crawlers welcome, AI included.
User-agent: *
Allow: /

Sitemap: ${base}/sitemap-index.xml
# AI-discovery map: ${base}/llms.txt
`;
  return new Response(body, {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });
};
