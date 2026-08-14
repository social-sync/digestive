// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	site: 'https://grimnir.danmatthews.me',
	integrations: [
		starlight({
			title: 'Grimnir',
			description:
				'A single-binary database exporter that anonymises, redacts, and ' +
				'deterministically hashes data on the way out.',
			social: [
				{
					icon: 'github',
					label: 'GitHub',
					href: 'https://github.com/danmatthews/grimnir',
				},
			],
			sidebar: [
				{ label: 'Installation', slug: 'installation' },
				{ label: 'Configuration', slug: 'configuration' },
				{ label: 'Transformers', slug: 'transformers' },
			],
		}),
	],
});
