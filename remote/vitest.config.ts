import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // The seeder is its own project under seeder/ with its own tests; both
    // trees run in one `pnpm test` so there is a single lane to keep green.
    include: ['test/**/*.test.ts', 'seeder/test/**/*.test.ts'],
    environment: 'node',
    // Stack synth (with esbuild bundling of the Lambdas) is slow on first run.
    testTimeout: 120_000,
  },
});
