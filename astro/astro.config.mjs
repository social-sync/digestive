// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	site: 'https://digestive.socialsync.tools',
	integrations: [
		starlight({
			title: 'Digestive',
			customCss: ['./src/styles/home.css'],
			description:
				'A single-binary database exporter that anonymises, redacts, and ' +
				'deterministically hashes data on the way out.',
			social: [
				{
					icon: 'github',
					label: 'GitHub',
					href: 'https://github.com/social-sync/digestive',
				},
			],
			sidebar: [
				{ label: 'Overview', slug: 'overview' },
				{ label: 'Installation', slug: 'installation' },
				{ label: 'Getting started', slug: 'getting-started' },
				{ label: 'Exports', slug: 'exports' },
				{ label: 'Restoring data to a database', slug: 'restoring' },
				{ label: 'Compliance Logs', slug: 'compliance-logs' },
				{ label: 'Comprehensive docs', slug: 'comprehensive-docs' },
				{ label: 'Command reference', slug: 'command-reference' },
			],
		}),
	],
});
