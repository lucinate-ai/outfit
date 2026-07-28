import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    environment: 'node',
    // Stack synth (with esbuild bundling of the Lambdas) is slow on first run.
    testTimeout: 120_000,
  },
});
