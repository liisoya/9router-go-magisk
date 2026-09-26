import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:20130',
      '/v1': 'http://localhost:20130',
      '/usage': 'http://localhost:20130',
      '/translator': 'http://localhost:20130',
      '/debug': 'http://localhost:20130',
      '/admin': 'http://localhost:20130',
    },
  },
})
