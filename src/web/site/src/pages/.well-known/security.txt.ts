import type { APIRoute } from 'astro';
import legal from '../../data/legal.json';

// Emits /.well-known/security.txt (RFC 9116). Contact + Policy come from
// legal.json / Astro.site, and Expires is recomputed to ~1 year out on every
// build so the file never goes stale (the RFC's common failure mode).
export const GET: APIRoute = ({ site }) => {
  const base = (site?.href ?? 'https://poddle.dev/').replace(/\/$/, '');
  const expires = new Date(Date.now() + 365 * 24 * 60 * 60 * 1000).toISOString();
  const body = `# Security contact for ${legal.entity} (${legal.productName}).
# We acknowledge good-faith reports within two business days - see the policy.
Contact: mailto:${legal.emails.security}
Expires: ${expires}
Policy: ${base}/security
Preferred-Languages: en
Canonical: ${base}/.well-known/security.txt
`;
  return new Response(body, {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });
};
