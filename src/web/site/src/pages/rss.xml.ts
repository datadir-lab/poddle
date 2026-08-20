import type { APIRoute } from 'astro';
import { getCollection, type CollectionEntry } from 'astro:content';

type Post = CollectionEntry<'blog'>;

// Minimal RSS 2.0 feed for the blog, hand-built (no runtime dep) - same pattern
// as robots.txt.ts / llms.txt.ts. Links follow the configured canonical domain.
const esc = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

export const GET: APIRoute = async ({ site }) => {
  const base = (site?.href ?? 'https://poddle.dev/').replace(/\/$/, '');
  const posts = (await getCollection('blog'))
    .filter((p: Post) => !p.data.draft)
    .sort((a: Post, b: Post) => b.data.pubDate.getTime() - a.data.pubDate.getTime());

  const items = posts
    .map(
      (p: Post) => `    <item>
      <title>${esc(p.data.title)}</title>
      <link>${base}/blog/${p.id}</link>
      <guid isPermaLink="true">${base}/blog/${p.id}</guid>
      <pubDate>${p.data.pubDate.toUTCString()}</pubDate>
      <description>${esc(p.data.description)}</description>
    </item>`,
    )
    .join('\n');

  const xml = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>poddle blog</title>
    <link>${base}/blog</link>
    <atom:link href="${base}/rss.xml" rel="self" type="application/rss+xml" />
    <description>Notes from the poddle team on secretless sandboxes for coding agents.</description>
    <language>en</language>
${items}
  </channel>
</rss>
`;

  return new Response(xml, {
    headers: { 'Content-Type': 'application/xml; charset=utf-8' },
  });
};
