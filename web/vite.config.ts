import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// In dev, the API comes from `go run ./cmd/dashboard-mock` (or the real agent).
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    proxy: {
      '/api': 'http://127.0.0.1:8080',
    },
  },
})
