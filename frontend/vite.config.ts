import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': 'http://127.0.0.1:18081',
      '/health': 'http://127.0.0.1:18081',
      '/auth': 'http://127.0.0.1:18082',
      '/dev': 'http://127.0.0.1:18082',
    },
  },
})
