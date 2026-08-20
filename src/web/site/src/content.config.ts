import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

// Blog posts are markdown in src/content/blog/. The loader id becomes the URL
// slug (/blog/<id>). Files starting with "_" are ignored, so drafts can sit in
// the folder without shipping.
const blog = defineCollection({
  loader: glob({ pattern: '**/[^_]*.md', base: './src/content/blog' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    pubDate: z.coerce.date(),
    author: z.string().default('The poddle team'),
    draft: z.boolean().default(false),
  }),
});

export const collections = { blog };
